#!/usr/bin/env python3
"""Migrate concrete RuntimeUnit methods to the required output contract."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess


def masked(source):
    """Retain code positions while hiding comments and literals."""
    chars = list(source)
    index = 0
    while index < len(source):
        start = index
        if source.startswith('//', index):
            index = source.find('\n', index)
            if index < 0:
                index = len(source)
        elif source.startswith('/*', index):
            depth = 1
            index += 2
            while index < len(source) and depth:
                if source.startswith('/*', index):
                    depth += 1
                    index += 2
                elif source.startswith('*/', index):
                    depth -= 1
                    index += 2
                else:
                    index += 1
            if depth:
                raise ValueError('unterminated comment')
        else:
            raw = re.match(r'(?:br|r)(#*)"', source[index:])
            char = re.match(r"'(?:\\(?:u\{[0-9a-fA-F]+\}|x[0-9a-fA-F]{2}|.)|[^'\\])'", source[index:])
            if raw:
                stop = source.find('"' + raw[1], index + raw.end())
                if stop < 0:
                    raise ValueError('unterminated raw string')
                index = stop + 1 + len(raw[1])
            elif char:
                index += char.end()
            elif source[index] == '"':
                index += 1
                while index < len(source):
                    if source[index] == '\\':
                        index += 2
                    elif source[index] == '"':
                        index += 1
                        break
                    else:
                        index += 1
                else:
                    raise ValueError('unterminated string')
            else:
                index += 1
                continue
        chars[start:index] = ['\n' if char == '\n' else ' ' for char in source[start:index]]
    return ''.join(chars)


def close_brace(code, start):
    depth = 0
    for index in range(start, len(code)):
        depth += (code[index] == '{') - (code[index] == '}')
        if depth == 0:
            return index
    raise ValueError('unbalanced implementation')


def migrate(source):
    code = masked(source)
    edits = []
    for implementation in re.finditer(r'\bimpl(?:\s*<[^{};]*>)?\s+(?:ovrt_unit::)?RuntimeUnit\s+for\s+[^{};]+\{', code):
        start = implementation.end()
        end = close_brace(code, start - 1)
        methods = list(re.finditer(r'\bfn\s+run\s*\(', code[start:end]))
        if not methods:
            continue
        if len(methods) != 1 or re.search(r'\bfn\s+execute\s*\(', code[start:end]):
            raise ValueError('review conflicting runtime methods')
        begin = start + methods[0].start()
        signature = re.match(r'fn\s+run\s*\(\s*&self\s*,\s*(\w+)\s*:\s*&\[u8\]\s*\)\s*->\s*Result\s*<\s*Vec\s*<\s*u8\s*>\s*,\s*String\s*>\s*\{', code[begin:end])
        if not signature:
            raise ValueError('review custom runtime method signature')
        body_start = begin + signature.end()
        body_end = close_brace(code, body_start - 1)
        body = source[body_start:body_end]
        if re.search(r'\b__ovrt_(?:output|result|run)\b', body):
            raise ValueError('review reserved migration identifiers')
        input_name = signature[1]
        echo = re.fullmatch(r'\s*Ok\((\w+(?:\.\w+)*)\.to_vec\(\)\)\s*', body)
        if echo and echo[1] == input_name:
            replacement = f'\n        __ovrt_output.write({input_name})\n    '
        else:
            replacement = ('\n        let __ovrt_run = || -> Result<Vec<u8>, String> {'
                           + body + '\n        };\n        __ovrt_output.write_owned(__ovrt_run()?)\n    ')
        signature = f'fn execute(&self, {input_name}: &[u8], __ovrt_output: &mut dyn ovrt_unit::RuntimeOutput) -> Result<(), String> {{'
        edits.append((begin, body_end + 1, signature + replacement + '}'))
    for start, end, replacement in reversed(edits):
        source = source[:start] + replacement + source[end:]
    return source, len(edits)


def plan(root, directories):
    changes = []
    for directory in directories:
        base = root / directory
        if not base.exists():
            continue
        if base.is_symlink() or not base.resolve().is_relative_to(root.resolve()):
            raise ValueError(f'review source directory: {base}')
        paths = []
        visited = 0
        for current, children, files in os.walk(base):
            children[:] = sorted(name for name in children
                                 if name not in {'target', 'node_modules', '.git'})
            visited += 1 + len(files)
            if visited > 16_384:
                raise ValueError(f'source traversal limit: {base}')
            paths.extend(Path(current) / name for name in files if name.endswith('.rs'))
        for path in sorted(paths):
            if path.is_symlink() or path.stat().st_size > 2_000_000:
                raise ValueError(f'review unsupported source: {path}')
            before = path.read_text()
            if not re.search(r'\bRuntimeUnit\s+for\b', before):
                continue
            after, count = migrate(before)
            if count:
                edition = '2021'
                for parent in path.parents:
                    manifest = parent / 'Cargo.toml'
                    if manifest.is_file():
                        match = re.search(r'^edition\s*=\s*"(2015|2018|2021|2024)"',
                                          manifest.read_text(), re.M)
                        if match:
                            edition = match[1]
                            break
                    if parent == root:
                        break
                formatted = subprocess.run(
                    ['rustfmt', '--edition', edition, '--emit', 'stdout',
                     '--config', 'skip_children=true'], input=after,
                    cwd=path.parent, capture_output=True, text=True, timeout=30)
                if formatted.returncode:
                    raise ValueError(f'Rust formatting failed for {path}: {formatted.stderr.strip()}')
                after = formatted.stdout
                changes.append((path, before, after, count))
    return changes


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('root', type=Path)
    parser.add_argument('--dry-run', action='store_true')
    parser.add_argument('--directory', action='append', default=[])
    parser.add_argument('--stale-locks', action='store_true')
    args = parser.parse_args()
    if args.stale_locks:
        for directory in ['rust', 'native/src-tauri', 'foundation/runtime-native/rust']:
            lock = args.root / directory / 'Cargo.lock'
            manifest = lock.with_name('Cargo.toml')
            if not lock.is_file() or not manifest.is_file():
                continue
            if any(path.is_symlink() or not path.resolve().is_relative_to(args.root.resolve())
                   for path in [lock, manifest]):
                raise ValueError(f'review lockfile location: {lock}')
            for package in lock.read_text().split('[[package]]'):
                if re.search(r'^name = "ovrt-unit"$', package, re.M) and '"arc-swap' not in package:
                    print(manifest)
        return
    changes = plan(args.root, args.directory or ['rust', 'native'])
    if args.dry_run:
        print(json.dumps([{'path': str(path.relative_to(args.root)), 'methods': count,
                           'beforeHash': hashlib.sha256(before.encode()).hexdigest()}
                          for path, before, _, count in changes], indent=2))
        return
    for path, before, after, count in changes:
        if path.read_text() != before:
            raise ValueError(f'source changed during migration: {path}')
        path.write_text(after)
        print(f'patched {path.relative_to(args.root)} migrated {count} runtime output methods')


if __name__ == '__main__':
    try:
        main()
    except (ValueError, OSError, subprocess.TimeoutExpired) as error:
        print(f'manual {error}')
        raise SystemExit(1) from error

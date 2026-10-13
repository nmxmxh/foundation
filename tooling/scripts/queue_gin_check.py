"""Reject new queue GIN indexes without requiring edits to legacy migrations."""

from pathlib import Path
import re
import subprocess
import sys

INDEX = re.compile(r'\bcreate\s+(?:unique\s+)?index\b[^;]*?\bon\s+(?:only\s+)?(?:(?:"[^"]+"|[a-z_][a-z_0-9]*)\s*\.\s*)?"?river_job"?\s+using\s+gin\b[^;]*;', re.I)
LEGACY = re.compile(r'create\s+index\s+(?:if\s+not\s+exists\s+)?river_job_(args|metadata)_index\s+on\s+river_job\s+using\s+gin\s*\(\s*\1\s*\)\s*;', re.I)
BASELINES = {'migrations/000001_init.up.sql', 'migrations/0001_schema.up.sql', 'migrations/000001_schema.up.sql',
             'migrations/0007_river_job_queue.up.sql'}


def check(root):
    result = subprocess.run(['rg', '--files', '--hidden', '-0', '-g', '*.sql',
                             '-g', '!**/node_modules/**', '-g', '!**/target/**', '-g', '!**/.git/**', str(root)],
                            capture_output=True, timeout=15)
    if result.returncode not in (0, 1):
        raise ValueError('SQL inventory failed')
    files = [Path(name.decode()) for name in result.stdout.split(b'\0') if name]
    if len(files) > 10000:
        raise ValueError('SQL inventory exceeds 10000 files')
    failures = []
    inspected_bytes = 0
    for path in files:
        size = path.stat().st_size
        inspected_bytes += size
        if size > 64 * 1024 * 1024 or inspected_bytes > 256 * 1024 * 1024:
            raise ValueError('SQL inventory exceeds 64 MiB per file or 256 MiB total')
        source = path.read_text()
        source = re.sub(r'/\*.*?\*/|--[^\n]*', '', source, flags=re.S)
        for match in INDEX.finditer(source):
            relative = path.relative_to(root).as_posix()
            historical = relative in BASELINES or relative.startswith('migrations/') and relative.endswith('.down.sql')
            if historical and LEGACY.fullmatch(match[0]):
                print(f'[WARN] legacy queue GIN index in {path.relative_to(root)}; review live queries before removal')
            else:
                failures.append(str(path.relative_to(root)))
    for name in sorted(set(failures)):
        print(f'[FAIL] new queue GIN index in {name}')
    return not failures


if __name__ == '__main__':
    try:
        sys.exit(0 if check(Path(sys.argv[1]).resolve()) else 1)
    except (ValueError, OSError, subprocess.TimeoutExpired) as error:
        print(f'[FAIL] queue index inspection: {error}', file=sys.stderr)
        sys.exit(1)

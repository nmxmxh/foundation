#!/usr/bin/env bash
# Managed-patch and shell-gate hygiene checks.
#
# Encodes two production incidents from the read-pressure optimization cycle:
#
#   1. `grep -F` treats its pattern literally, so a regex escape such as `\*`
#      matches a literal backslash-asterisk. A patched script gated cluster
#      edits behind `grep -Fq '...\*...'` and the gates never fired. Rule:
#      inside a -F pattern, no backslash before a regex metacharacter
#      (* + . ? ( ) [ ] { } |). Drop the escape, or add a `grep-f-ok`
#      waiver comment on the line. `$'...'` segments are shell-interpreted
#      and therefore exempt.
#
#   2. Patch wiring drifts silently. Rule: every patch_* function defined in
#      scaffold_managed_patches.sh must have a call site (arguments allowed),
#      every call site must resolve to a definition, and every definition
#      should carry a preceding @since tag.
#
# Usage: check_managed_patches.sh [scripts-dir]   (default: tooling/scripts)

set -uo pipefail

dir="${1:-tooling/scripts}"
if [[ ! -d "$dir" ]]; then
  echo "no scripts dir: $dir" >&2
  exit 2
fi

exec python3 - "$dir" <<'PYEOF'
import os
import re
import sys

scripts_dir = sys.argv[1]
self_name = "check_managed_patches.sh"
failures = []
warnings = []

METACHAR_ESCAPE = re.compile(r"\\[*+.?(){}|\[\]]")
QUOTED = re.compile(r"""(?<!\$)(["'])((?:(?!\1).)*)\1""")
GREP_F = re.compile(r"\bgrep\s+(?:[^|;\n]*\s)?-(?:[a-zA-Z]+)?F")

for name in sorted(os.listdir(scripts_dir)):
    if not name.endswith(".sh") or name == self_name:
        continue
    path = os.path.join(scripts_dir, name)
    with open(path, encoding="utf-8", errors="replace") as handle:
        original = handle.read()

    # Fold backslash continuations while remembering each logical line's
    # physical start number, so a trailing "\" on a wrapped command is not
    # mistaken for part of a pattern.
    folded = []
    buf = ""
    start = None
    for lineno, line in enumerate(original.split("\n"), start=1):
        if start is None:
            start = lineno
        if line.endswith("\\"):
            buf += line[:-1] + "  "
            continue
        folded.append((start, buf + line))
        buf = ""
        start = None
    if buf:
        folded.append((start, buf))

    for lineno, text in folded:
        if "grep-f-ok" in text or not GREP_F.search(text):
            continue
        for chunk in re.split(r"(?=\bgrep\b)", text):
            if not GREP_F.search(chunk):
                continue
            for match in QUOTED.finditer(chunk):
                if METACHAR_ESCAPE.search(match.group(2)):
                    failures.append(
                        "%s:%d: grep -F pattern holds a regex escape "
                        "(matches literally): %s" % (path, lineno, text.strip())
                    )
                    break

patches_file = os.path.join(scripts_dir, "scaffold_managed_patches.sh")
if os.path.isfile(patches_file):
    with open(patches_file, encoding="utf-8") as handle:
        lines = handle.read().split("\n")

    def_re = re.compile(r"^(patch_[a-z0-9_]+)\(\)\s*\{?\s*$")
    call_re = re.compile(r"^(patch_[a-z0-9_]+)(?:\s|$)")
    defined = set()
    calls_only = set()
    for ln in lines:
        m = def_re.match(ln)
        if m:
            defined.add(m.group(1))
            continue
        m = call_re.match(ln)
        if m:
            calls_only.add(m.group(1))

    for fn in sorted(defined - calls_only):
        failures.append(
            "%s: patch function defined but never invoked: %s" % (patches_file, fn)
        )
    for fn in sorted(calls_only - defined):
        failures.append("%s: patch invoked but not defined: %s" % (patches_file, fn))

    prev = ""
    untagged = 0
    for ln in lines:
        if re.match(r"^patch_[a-z0-9_]+\(\)\s*\{?\s*$", ln):
            if "@since" not in prev:
                untagged += 1
        prev = ln
    if untagged:
        warnings.append("%d patch function(s) lack a preceding @since tag" % untagged)

for warning in warnings:
    print("[WARN] " + warning)
for failure in failures:
    print("[FAIL] " + failure, file=sys.stderr)
if failures:
    print("%d managed-patch hygiene failure(s)" % len(failures), file=sys.stderr)
    sys.exit(1)
print("managed-patch hygiene OK (%s)" % scripts_dir)
PYEOF

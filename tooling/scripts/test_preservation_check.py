#!/usr/bin/env python3
"""Verify a package's test count is unchanged by a consolidation.

Consolidation moves test functions between files and deletes donors. A move that
silently drops a test looks exactly like a move that worked, and a lost test is
a silent loss of coverage. This compares the set of test function names before
and after so the claim "consolidation changed nothing but layout" is checkable
rather than asserted.

Usage:
  test_preservation_check.py <git-ref> <package-dir> [<package-dir>...]

<git-ref> is the commit holding the pre-consolidation tree, usually HEAD when the
work is uncommitted.
"""
import re
import subprocess
import sys
from pathlib import Path

FUNC = re.compile(r"(?m)^func ((?:Test|Benchmark|Example|Fuzz)\w+)\(")


def names_in_dir(root: Path) -> set:
    """Test names under root, including subpackages.

    The walk must be recursive to match `git ls-tree -r`. A non-recursive scan
    silently drops every subpackage test and then reports them as lost, which is
    worse than no check at all.
    """
    found = set()
    if not root.is_dir():
        return found
    for f in root.rglob("*_test.go"):
        found |= set(FUNC.findall(f.read_text(encoding="utf-8", errors="replace")))
    return found


def names_at_ref(ref: str, root: Path) -> set:
    out = subprocess.run(
        ["git", "ls-tree", "-r", "--name-only", ref, "--", str(root)],
        capture_output=True, text=True, check=True,
    ).stdout.split()
    found = set()
    for rel in out:
        if not rel.endswith("_test.go"):
            continue
        blob = subprocess.run(
            ["git", "show", f"{ref}:{rel}"], capture_output=True, text=True
        ).stdout
        found |= set(FUNC.findall(blob))
    return found


def main() -> int:
    if len(sys.argv) < 3:
        print(__doc__, file=sys.stderr)
        return 2
    ref, dirs = sys.argv[1], sys.argv[2:]
    failed = False
    for d in dirs:
        root = Path(d)
        before = names_at_ref(ref, root)
        after = names_in_dir(root)
        lost = sorted(before - after)
        added = sorted(after - before)
        if lost:
            print(f"[FAIL] {d}: {len(lost)} test(s) lost in consolidation")
            for name in lost:
                print(f"  lost: {name}")
            failed = True
        if added:
            print(f"[WARN] {d}: {len(added)} test(s) added (expected for a new guard)")
        if not lost:
            print(f"[OK] {d}: all {len(before)} pre-existing tests preserved")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())

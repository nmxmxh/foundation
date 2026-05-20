#!/bin/zsh
set -euo pipefail

target="${1:-.}"

python3 - "$target" <<'PY'
from __future__ import annotations

import pathlib
import re
import sys

target = pathlib.Path(sys.argv[1]).resolve()
roots = [target / "internal" / "service", target / "backend" / "internal" / "service", target / "internal" / "server", target / "backend" / "internal" / "server"]
violations: list[str] = []

skip_parts = {"foundation", "vendor", "node_modules", ".cache", "dist", "build", "generated", "testdata"}

def should_skip(path: pathlib.Path) -> bool:
    if path.name.endswith("_test.go"):
        return True
    return any(part in skip_parts for part in path.parts)

def find_matching_brace(text: str, open_index: int) -> int:
    depth = 0
    in_string: str | None = None
    escaped = False
    for idx in range(open_index, len(text)):
        ch = text[idx]
        if in_string:
            if escaped:
                escaped = False
                continue
            if ch == "\\":
                escaped = True
                continue
            if ch == in_string:
                in_string = None
            continue
        if ch in ('"', "'", "`"):
            in_string = ch
            continue
        if ch == "{":
            depth += 1
        elif ch == "}":
            depth -= 1
            if depth == 0:
                return idx
    return -1

for root in roots:
    if not root.exists():
        continue
    for path in root.rglob("*.go"):
        if should_skip(path):
            continue
        text = path.read_text()
        for match in re.finditer(r"events\.Envelope\s*\{", text):
            open_index = text.find("{", match.start())
            close_index = find_matching_brace(text, open_index)
            if close_index < 0:
                continue
            block = text[open_index + 1 : close_index]
            if not block.strip():
                continue
            line = text.count("\n", 0, match.start()) + 1
            rel = path.relative_to(target)
            if not re.search(r"\bMetadata\s*:", block):
                if "internal/service" in rel.as_posix():
                    continue
                violations.append(f"{rel}:{line}: events.Envelope must carry canonical metadata")
                continue
            if re.search(r"\bMetadata\s*:\s*(?:nil|map\s*\[\s*string\s*\]\s*(?:any|interface\s*\{\s*\})\s*\{)", block):
                violations.append(f"{rel}:{line}: events.Envelope metadata must come from server-kit metadata helpers, not raw map literals")
        if "internal/service" in path.relative_to(target).as_posix():
            for match in re.finditer(r"metadata\.FromContext\(ctx\)\.ToMap\(\)", text):
                line = text.count("\n", 0, match.start()) + 1
                rel = path.relative_to(target)
                violations.append(f"{rel}:{line}: use metadata.FromContextMap(ctx, domainMetadata...) so tags/categories merge centrally")
            for match in re.finditer(r"\bMetadata\s*:\s*req\.Metadata\b", text):
                line = text.count("\n", 0, match.start()) + 1
                rel = path.relative_to(target)
                violations.append(f"{rel}:{line}: request metadata must be merged with context metadata via metadata.FromContextMap(ctx, req.Metadata)")
            for match in re.finditer(r"\bMetadata\s*:\s*map\s*\[\s*string\s*\]\s*(?:any|interface\s*\{\s*\})\s*\{\s*\}", text):
                line = text.count("\n", 0, match.start()) + 1
                rel = path.relative_to(target)
                violations.append(f"{rel}:{line}: persisted domain metadata must inherit request metadata or carry explicit domain metadata, not an empty map")
            for match in re.finditer(r"(?is)\btags\s*:\s*\[\]string\s*\{([^}]*)\}", text):
                if re.search(r"(?i)\b(authorization|bearer|cookie|jwt|password|private_key|secret|session_token|token)\b", match.group(1)):
                    line = text.count("\n", 0, match.start()) + 1
                    rel = path.relative_to(target)
                    violations.append(f"{rel}:{line}: metadata tags must not contain credentials, tokens, cookies, or secret-bearing values")

if violations:
    print("[FAIL] service event metadata practices")
    print("  events.Envelope emissions must preserve canonical request metadata via server-kit/go/metadata.")
    for violation in violations[:80]:
        print(f"  {violation}")
    if len(violations) > 80:
        print(f"  ... {len(violations) - 80} more")
    sys.exit(1)

print("[OK] service event metadata practices")

proto_root = target / "api" / "protos" / "common" / "v1"
metadata_proto = proto_root / "metadata.proto"
if not metadata_proto.exists():
    metadata_proto = proto_root / "common.proto"
if metadata_proto.exists():
    proto_text = metadata_proto.read_text()
    if not re.search(r"\brepeated\s+string\s+tags\s*=", proto_text):
        violations.append("api/protos/common/v1/metadata.proto: common metadata must expose repeated string tags")
    if not re.search(r"\brepeated\s+string\s+categories\s*=", proto_text):
        violations.append("api/protos/common/v1/metadata.proto: common metadata must expose repeated string categories")
    if not re.search(r"\bstring\s+knowledge_graph\s*=", proto_text):
        violations.append("api/protos/common/v1/metadata.proto: common metadata must expose knowledge_graph for intelligence graph scope")
    if not re.search(r"\bmap\s*<\s*string\s*,\s*string\s*>\s+attributes\s*=", proto_text):
        violations.append("api/protos/common/v1/metadata.proto: common metadata must expose map<string,string> attributes for bounded graph facts")

migrations = target / "migrations"
if migrations.exists():
    tag_query = False
    tag_index = False
    for path in migrations.rglob("*.up.sql"):
        rel = path.relative_to(target)
        text = path.read_text()
        for match in re.finditer(r"(?im)^[ \t]*metadata[ \t]+jsonb\b[^\n,]*(?:,|$)", text):
            line_text = match.group(0)
            upper = line_text.upper()
            if "NOT NULL" not in upper or "DEFAULT" not in upper or "'{}'" not in line_text:
                line = text.count("\n", 0, match.start()) + 1
                violations.append(f"{rel}:{line}: metadata jsonb must be NOT NULL DEFAULT '{{}}'::jsonb")
        if re.search(r"metadata\s*->\s*'tags'|metadata\s*->>\s*'tags'|metadata\s*@>", text):
            tag_query = True
        if re.search(r"USING\s+GIN\s*\(\s*\(?\s*metadata\s*(?:jsonb_path_ops|->\s*'tags'|)\s*\)?", text, re.IGNORECASE) or re.search(r"metadata_tags.*GIN|tags.*GIN", text, re.IGNORECASE):
            tag_index = True
    if tag_query and not tag_index:
        violations.append("migrations: metadata tag queries require a matching GIN or metadata->'tags' expression index")

if violations:
    print("[FAIL] metadata practices")
    print("  Metadata must be canonical, tag-capable, and queryable when tags are used.")
    for violation in violations[:100]:
        print(f"  {violation}")
    if len(violations) > 100:
        print(f"  ... {len(violations) - 100} more")
    sys.exit(1)

print("[OK] metadata proto/schema practices")
PY

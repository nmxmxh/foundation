#!/usr/bin/env bash
# The worker guard distinguishes domain enums from queue implementations.
set -euo pipefail
core_root="$(cd "$(dirname "$0")/.." && pwd)"
queue_fixture="$(mktemp -d "${TMPDIR:-/tmp}/foundation-queue-guard.XXXXXX")"
trap 'rm -rf "$queue_fixture"' EXIT
mkdir -p "$queue_fixture/internal/service/example"
touch "$queue_fixture/.foundation"
printf 'module test.local/queueguard\n\ngo 1.26\n' > "$queue_fixture/go.mod"
for declaration in 'type QueueDecision int' 'type QueueState uint8'; do
 printf 'package example\n%s\n' "$declaration" > "$queue_fixture/internal/service/example/queue.go"
 zsh "$core_root/tooling/scripts/server_kit_usage_check.sh" "$queue_fixture" > "$queue_fixture/result" 2>&1 || true
 grep -q '\[OK\] services use Foundation worker/runtime primitives' "$queue_fixture/result"
done
for declaration in 'type LocalQueue struct{}' 'type LocalQueue []string' 'type LocalQueue map[string]string' 'type LocalQueue chan string' 'type LocalQueue interface{}'; do
 printf 'package example\n%s\n' "$declaration" > "$queue_fixture/internal/service/example/queue.go"
 zsh "$core_root/tooling/scripts/server_kit_usage_check.sh" "$queue_fixture" > "$queue_fixture/result" 2>&1 || true
 grep -q '\[FAIL\] services use Foundation worker/runtime primitives' "$queue_fixture/result"
done
mkdir -p "$queue_fixture/internal/adapters"
zsh "$core_root/tooling/scripts/server_kit_usage_check.sh" "$queue_fixture" > "$queue_fixture/result" 2>&1 || true
grep -q '\[FAIL\] application internals avoid adapter package/file shapes' "$queue_fixture/result"
printf 'Server-kit queue guard contracts passed\n' 

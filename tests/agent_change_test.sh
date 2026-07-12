#!/bin/bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP="$(mktemp -d /tmp/foundation-agent-change.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT
TOOL="$ROOT/tooling/scripts/agent_change.mjs"
PLAN="$TMP/review-task.json"
EVIDENCE="$TMP/review-task.evidence.json"

node "$TOOL" graph --capability live_projection | grep -q '"owner": "runtime-transport"'
node "$TOOL" plan --feature review-task --commands create,assign,complete --projection list --offline --realtime --out "$PLAN"
node "$TOOL" check --file "$PLAN"
node "$TOOL" evidence --plan "$PLAN" --out "$EVIDENCE"

node - "$PLAN" "$EVIDENCE" <<'NODE'
const fs = require("node:fs");
const plan = JSON.parse(fs.readFileSync(process.argv[2], "utf8"));
const evidence = JSON.parse(fs.readFileSync(process.argv[3], "utf8"));
if (plan.risk !== "tier2" || plan.approval !== "review") throw new Error("domain feature risk classification mismatch");
if (plan.commands.some((command) => !command.requested || !command.success || !command.failed)) throw new Error("terminal lifecycle incomplete");
if (!plan.worker.bounded || plan.fallback !== "HTTP after WebSocket loss") throw new Error("bounded/fallback model missing");
if (evidence.objective !== plan.objective || evidence.invariants.length === 0) throw new Error("evidence scaffold mismatch");
NODE

node "$TOOL" plan --feature auth-policy --commands update --projection policy --out "$TMP/auth.json"
grep -q '"approval": "required"' "$TMP/auth.json"

node - "$PLAN" <<'NODE'
const fs = require("node:fs");
const file = process.argv[2];
const plan = JSON.parse(fs.readFileSync(file, "utf8"));
delete plan.commands[0].failed;
fs.writeFileSync(file, JSON.stringify(plan));
NODE
if node "$TOOL" check --file "$PLAN" >/dev/null 2>&1; then
  echo "invalid terminal lifecycle unexpectedly passed" >&2
  exit 1
fi

echo "foundation agent change contract passed"

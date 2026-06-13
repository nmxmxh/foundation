#!/usr/bin/env node
/**
 * generate_lifecycle_manifest.mjs
 *
 * Scans api/protos for mutating command pairs and emits two artifacts:
 *
 *   1. docs/references/lifecycle/lifecycle_contract.json
 *      Machine-readable contract manifest consumed by agents, linters, and
 *      test generators.
 *
 *   2. docs/references/lifecycle/lifecycle_contract_guide.md
 *      Human+agent reference cheat sheet that lists every known event name,
 *      job kind, queue, and review vector.
 *
 * Usage:
 *   node tooling/scripts/generate_lifecycle_manifest.mjs [options]
 *
 * Options:
 *   --proto-root <dir>    Proto root. Default: api/protos
 *   --manifest-out <f>   JSON output. Default: docs/references/lifecycle/lifecycle_contract.json
 *   --guide-out <f>      Markdown output. Default: docs/references/lifecycle/lifecycle_contract_guide.md
 *   --include-template    Include api/protos/_template fixtures
 *   --check              Fail if either output is missing or stale (CI mode)
 *   --help               Print this message
 *
 * The JSON schema is documented in docs/references/lifecycle/lifecycle_contract_guide.md.
 */

import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  statSync,
  writeFileSync,
} from "node:fs";
import path from "node:path";
import process from "node:process";

// ── Action vocabulary (kept in sync with generate_lifecycle_contract_tests.mjs) ──
const MUTATING_ACTIONS = new Set([
  "accept", "activate", "add", "apply", "approve", "archive", "assign",
  "authenticate", "cancel", "close", "complete", "confirm", "create",
  "deactivate", "delete", "disable", "enable", "execute", "generate",
  "import", "invite", "issue", "merge", "patch", "pause", "process",
  "publish", "reject", "remove", "restore", "resume", "revoke", "run",
  "send", "set", "start", "stop", "submit", "sync", "update", "upsert",
  "upload",
]);

const READ_ONLY_ACTIONS = new Set([
  "count", "describe", "find", "get", "list", "query", "read", "search", "watch",
]);

const KNOWN_ACTIONS = [...MUTATING_ACTIONS, ...READ_ONLY_ACTIONS].sort(
  (a, b) => b.length - a.length
);

const REVIEW_VECTOR_IDS = [
  "tenant_isolation",
  "correlation_id_propagation",
  "idempotency",
  "requested_before_terminal",
  "bounded_work",
  "fallback_path",
];

const INVARIANTS = [
  "MetadataPreserved",
  "TenantScopeStable",
  "RequestedBeforeTerminal",
  "ExactlyOneTerminalVisible",
  "IdempotentRetry",
  "BoundedWork",
  "FallbackRefinement",
  "ProjectionAfterTerminal",
];

// ── Main ──────────────────────────────────────────────────────────────────────

function main() {
  const args = parseArgs(process.argv.slice(2));

  const protoRootArg = args.protoRoot ?? "api/protos";
  const protoRoot = path.resolve(protoRootArg);
  const protoRootLabel = stableProtoRootLabel(protoRootArg, protoRoot);
  const manifestOut = path.resolve(
    args.manifestOut ?? "docs/references/lifecycle/lifecycle_contract.json"
  );
  const guideOut = path.resolve(
    args.guideOut ?? "docs/references/lifecycle/lifecycle_contract_guide.md"
  );

  const includeTemplate = Boolean(args.includeTemplate);
  const { contracts, errors } = discoverContracts(protoRoot, includeTemplate);

  if (errors.length > 0) {
    for (const e of errors) console.error(`[lifecycle-manifest] ${e}`);
    process.exit(1);
  }

  const manifest = buildManifest(contracts, protoRootLabel, includeTemplate);
  validateManifest(manifest);
  const manifestJSON = JSON.stringify(manifest, null, 2) + "\n";
  const guideMarkdown = buildGuide(manifest);

  if (args.check) {
    let ok = true;

    if (!existsSync(manifestOut)) {
      console.error(`[FAIL] lifecycle manifest missing: ${rel(manifestOut)}`);
      console.error("  run: make lifecycle-manifest");
      ok = false;
    } else if (readFileSync(manifestOut, "utf8") !== manifestJSON) {
      console.error(`[FAIL] lifecycle manifest stale: ${rel(manifestOut)}`);
      console.error("  run: make lifecycle-manifest");
      ok = false;
    } else {
      console.log(`[OK] lifecycle manifest current: ${rel(manifestOut)}`);
    }

    if (!existsSync(guideOut)) {
      console.error(`[FAIL] lifecycle guide missing: ${rel(guideOut)}`);
      console.error("  run: make lifecycle-manifest");
      ok = false;
    } else if (readFileSync(guideOut, "utf8") !== guideMarkdown) {
      console.error(`[FAIL] lifecycle guide stale: ${rel(guideOut)}`);
      console.error("  run: make lifecycle-manifest");
      ok = false;
    } else {
      console.log(`[OK] lifecycle guide current: ${rel(guideOut)}`);
    }

    if (!ok) process.exit(1);
    return;
  }

  mkdirSync(path.dirname(manifestOut), { recursive: true });
  writeFileSync(manifestOut, manifestJSON);
  console.log(
    `[OK] wrote ${manifest.contracts.length} lifecycle contracts → ${rel(manifestOut)}`
  );

  mkdirSync(path.dirname(guideOut), { recursive: true });
  writeFileSync(guideOut, guideMarkdown);
  console.log(`[OK] wrote lifecycle guide → ${rel(guideOut)}`);
}

// ── Manifest builder ──────────────────────────────────────────────────────────

function buildManifest(contracts, protoRootLabel, includeTemplate) {
  return {
    $schema: "foundation/lifecycle-contract/v1",
    schema_version: 1,
    generated_by: "tooling/scripts/generate_lifecycle_manifest.mjs",
    doc: "docs/references/lifecycle/lifecycle_contract_guide.md",
    source: {
      proto_root: protoRootLabel,
      discovery: "mutating request/response pairs with foundation.v1.Metadata metadata = 1",
      include_template: includeTemplate,
      mutating_actions: [...MUTATING_ACTIONS].sort(),
    },
    invariants: INVARIANTS,
    review_vector_ids: REVIEW_VECTOR_IDS,
    contracts: contracts.map((c) => ({
      id: `${c.domain}:${c.action}:${c.version}`,
      domain: c.domain,
      action: c.action,
      version: c.version,
      source_proto: c.source,
      request_message: c.request,
      response_message: c.response,
      events: {
        requested: `${c.domain}:${c.action}:${c.version}:requested`,
        success: `${c.domain}:${c.action}:${c.version}:success`,
        failed: `${c.domain}:${c.action}:${c.version}:failed`,
      },
      worker: {
        job_kind: `${c.domain}.${c.action}`,
        queue: c.domain,
      },
      review_vectors: buildReviewVectors(c),
    })),
  };
}

function buildReviewVectors(c) {
  return [
    {
      id: "tenant_isolation",
      description: `${c.request} must derive organization scope from authenticated context, never from client-supplied organization_id.`,
      invariant: "TenantScopeStable",
      test_hint: `assert that a request with spoofed organization_id returns 403 or is ignored`,
    },
    {
      id: "correlation_id_propagation",
      description: `All jobs and events emitted by ${c.domain}:${c.action}:${c.version} must carry the same correlation_id as the originating ${c.request}.`,
      invariant: "MetadataPreserved",
      test_hint: `inspect observed jobs and terminal events for matching correlation_id`,
    },
    {
      id: "idempotency",
      description: `Duplicate submissions of ${c.request} with the same idempotency_key must not create duplicate side effects.`,
      invariant: "IdempotentRetry",
      test_hint: `submit ${c.request} twice with identical idempotency_key; assert exactly one terminal event`,
    },
    {
      id: "requested_before_terminal",
      description: `Event ${c.domain}:${c.action}:${c.version}:requested must be emitted before :success or :failed is visible to subscribers.`,
      invariant: "RequestedBeforeTerminal",
      test_hint: `capture event bus output and assert requested timestamp < terminal timestamp`,
    },
    {
      id: "bounded_work",
      description: `All retries, timeouts, and worker attempts for ${c.domain}.${c.action} must have explicit finite bounds.`,
      invariant: "BoundedWork",
      test_hint: `verify MaxAttempts, timeout, and context deadline are set on the worker job`,
    },
    {
      id: "fallback_path",
      description: `Failure of the ${c.domain}:${c.action}:${c.version} command must emit :failed, never silently swallow the error.`,
      invariant: "ExactlyOneTerminalVisible",
      test_hint: `inject handler failure; assert :failed event is emitted with error_class set`,
    },
  ];
}

function validateManifest(manifest) {
  const seen = new Set();
  for (const contract of manifest.contracts) {
    if (seen.has(contract.id)) {
      throw new Error(`duplicate lifecycle contract id: ${contract.id}`);
    }
    seen.add(contract.id);

    const expectedEvents = {
      requested: `${contract.domain}:${contract.action}:${contract.version}:requested`,
      success: `${contract.domain}:${contract.action}:${contract.version}:success`,
      failed: `${contract.domain}:${contract.action}:${contract.version}:failed`,
    };
    for (const [state, eventName] of Object.entries(expectedEvents)) {
      if (contract.events[state] !== eventName) {
        throw new Error(`${contract.id} has invalid ${state} event: ${contract.events[state]}`);
      }
    }

    const vectorIDs = contract.review_vectors.map((item) => item.id);
    for (const required of REVIEW_VECTOR_IDS) {
      if (!vectorIDs.includes(required)) {
        throw new Error(`${contract.id} missing review vector: ${required}`);
      }
    }
  }
}

// ── Guide builder ─────────────────────────────────────────────────────────────

function buildGuide(manifest) {
  const count = manifest.contracts.length;

  const contractRows = manifest.contracts
    .map((c) => {
      const events = Object.values(c.events).join("<br>");
      return `| \`${c.id}\` | \`${c.worker.job_kind}\` | \`${c.worker.queue}\` | ${events} | \`${c.source_proto}\` |`;
    })
    .join("\n");

  const agentChecklist = manifest.contracts
    .flatMap((c) =>
      c.review_vectors.map(
        (v) =>
          `- **${c.id} / ${v.id}**: ${v.description}\n  - Test hint: \`${v.test_hint}\`\n  - Invariant: \`${v.invariant}\``
      )
    )
    .join("\n\n");

  return `# Foundation Lifecycle Contract Guide

> Generated by \`tooling/scripts/generate_lifecycle_manifest.mjs\`
> Source manifest: \`docs/references/lifecycle/lifecycle_contract.json\`
> Proto root: \`${manifest.source.proto_root}\`
> Total contracts: **${count}**

---

## What This Is

This file is the **human + agent cheat sheet** for all mutating command
lifecycles discovered in \`${manifest.source.proto_root}\`. It is generated from the proto
definitions and must be kept current with \`make lifecycle-manifest\`.

The machine-readable version is \`docs/references/lifecycle/lifecycle_contract.json\`. Agents and
linters should parse the JSON, not this Markdown.

The output is deterministic by design. It does not embed wall-clock generation
time, so \`make check-lifecycle-manifest\` only fails on contract drift.

---

## JSON Schema (v1)

\`\`\`jsonc
{
  "$schema": "foundation/lifecycle-contract/v1",
  "schema_version": 1,
  "source": {
    "proto_root": "api/protos",
    "discovery": "mutating request/response pairs with foundation.v1.Metadata metadata = 1",
    "include_template": false,
    "mutating_actions": ["create", "update"]
  },
  "invariants": ["MetadataPreserved"],
  "review_vector_ids": ["tenant_isolation"],
  "contracts": [
    {
      "id": "<domain>:<action>:<version>",          // unique contract ID
      "domain": "string",                            // event domain
      "action": "string",                            // verb (create, update, …)
      "version": "v1",                               // proto package version
      "source_proto": "path/relative/to/api/protos", // authoritative source
      "request_message": "CreateFooRequest",
      "response_message": "CreateFooResponse",
      "events": {
        "requested": "<domain>:<action>:<version>:requested",
        "success":   "<domain>:<action>:<version>:success",
        "failed":    "<domain>:<action>:<version>:failed"
      },
      "worker": {
        "job_kind": "<domain>.<action>",             // River job kind
        "queue":    "<domain>"                        // River queue name
      },
      "review_vectors": [                            // agent review checklist
        {
          "id": "tenant_isolation",
          "description": "...",
          "invariant": "TenantScopeStable",
          "test_hint": "..."
        }
        // … one per nervous-system invariant
      ]
    }
  ]
}
\`\`\`

---

## Contract Table

| Contract ID | Job Kind | Queue | Events | Proto Source |
|---|---|---|---|---|
${contractRows}

---

## Agent Review Vectors

For every discovered contract, the following review vectors must be satisfied
before marking a command handler as done. \`make check-lifecycle-manifest\`
keeps the machine-readable source and this guide current.

${count === 0 ? "_No mutating contracts discovered yet. Add proto files under `api/protos` and regenerate._" : agentChecklist}

---

## How To Use This In Agent Workflows

### Reading the manifest

\`\`\`js
// In a tooling script or agent context:
const manifest = JSON.parse(fs.readFileSync("docs/references/lifecycle/lifecycle_contract.json", "utf8"));
for (const contract of manifest.contracts) {
  console.log(contract.events.requested); // e.g. "order:create:v1:requested"
  console.log(contract.review_vectors);   // one per invariant
}
\`\`\`

### Generating event names

Do not hand-write event strings. Derive them:

\`\`\`go
// In Go — derive from the contract, do not string-compose manually:
eventType := fmt.Sprintf("%s:%s:%s:requested", domain, action, version)
// or use the generated contracttest constants from generated_lifecycle_test.go
\`\`\`

### Using review vectors as a handoff checklist

Agents producing a command handler must include in their handoff note:

\`\`\`
Contract: order:create:v1
Evidence:
  - tenant_isolation: cross-org request tested in TestCreateOrder_TenantBleed
  - idempotency: duplicate key test in TestCreateOrder_Idempotent
  - correlation_id_propagation: job.CorrelationID asserted in TestCreateOrder_JobMetadata
  - requested_before_terminal: event order asserted in TestCreateOrder_Lifecycle
  - bounded_work: MaxAttempts=3, timeout=30s verified in TestCreateOrder_WorkerBounds
  - fallback_path: injected handler error, :failed observed in TestCreateOrder_Failure
\`\`\`

---

## Regenerating

\`\`\`bash
make lifecycle-manifest       # regenerate both JSON and this guide
make check-lifecycle-manifest # CI: fail if stale
\`\`\`

---

## Maintenance Rules

1. Do not hand-edit \`lifecycle_contract.json\` or this file. Regenerate.
2. Scaffold template protos under \`_template/\` are ignored by default. Use
   \`--include-template\` only for generator development.
3. When adding a new proto file with mutating commands, run \`make lifecycle-manifest\`.
4. The manifest is a source of truth for event names. If an event name in code
   does not match the manifest, the code is wrong.
5. Review vectors are fixed for v1 of the manifest schema. Adding a new vector
   is non-breaking. Removing or renaming one requires a schema version bump.
`;
}

// ── Proto discovery (shared with generate_lifecycle_contract_tests.mjs) ───────

function discoverContracts(protoRoot, includeTemplate) {
  const contracts = [];
  const errors = [];
  if (!existsSync(protoRoot)) return { contracts, errors };

  for (const file of findProtoFiles(protoRoot)) {
    const normalized = file.split(path.sep).join("/");
    if (!includeTemplate && normalized.includes("/_template/")) {
      continue;
    }
    const parsed = parseProtoFile(file, protoRoot);
    errors.push(...parsed.errors);
    contracts.push(...parsed.contracts);
  }

  contracts.sort((a, b) => {
    const l = `${a.domain}:${a.action}:${a.version}`;
    const r = `${b.domain}:${b.action}:${b.version}`;
    return l.localeCompare(r);
  });

  return { contracts, errors };
}

function findProtoFiles(root) {
  const files = [];
  walk(root, files);
  return files.sort();
}

function walk(dir, files) {
  for (const entry of readdirSync(dir).sort()) {
    const full = path.join(dir, entry);
    const stat = statSync(full);
    if (stat.isDirectory()) { walk(full, files); continue; }
    if (entry.endsWith(".proto")) files.push(full);
  }
}

function parseProtoFile(file, protoRoot) {
  const text = readFileSync(file, "utf8");
  const relFile = path.relative(protoRoot, file).split(path.sep).join("/");
  const pkg = matchOne(text, /^\s*package\s+([A-Za-z0-9_.]+)\s*;/m);
  const errors = [];
  const contracts = [];
  if (!pkg) return { contracts, errors };

  const parts = pkg.split(".");
  const vi = parts.findIndex((p) => /^v[0-9]+$/.test(p));
  if (vi <= 0) return { contracts, errors };

  const domain = lowerSnake(parts.slice(0, vi).join("_"));
  const version = parts[vi];
  if (domain === "common" || domain === "transport" || domain === "foundation") {
    return { contracts, errors };
  }

  const messages = parseMessages(text);
  const messageNames = new Set(messages.map((m) => m.name));

  for (const msg of messages) {
    if (!msg.name.endsWith("Request")) continue;
    const operation = msg.name.slice(0, -"Request".length);
    const verb = actionFromOperation(operation);
    if (!MUTATING_ACTIONS.has(verb)) continue;
    const action = lowerSnake(operation);

    if (!hasFoundationMetadata(msg.body)) {
      errors.push(`${relFile}: ${msg.name} must declare foundation.v1.Metadata metadata = 1`);
      continue;
    }

    const responseName = `${operation}Response`;
    if (!messageNames.has(responseName)) {
      errors.push(`${relFile}: ${msg.name} must have matching ${responseName}`);
      continue;
    }

    contracts.push({ domain, action, version, source: relFile, request: msg.name, response: responseName });
  }

  return { contracts, errors };
}

function parseMessages(text) {
  const messages = [];
  const re = /\bmessage\s+([A-Za-z0-9_]+)\s*\{/g;
  let m;
  while ((m = re.exec(text)) !== null) {
    const name = m[1];
    const bodyStart = re.lastIndex;
    const bodyEnd = findMatchingBrace(text, bodyStart - 1);
    if (bodyEnd < 0) continue;
    messages.push({ name, body: text.slice(bodyStart, bodyEnd) });
    re.lastIndex = bodyEnd + 1;
  }
  return messages;
}

function findMatchingBrace(text, openIndex) {
  let depth = 0;
  for (let i = openIndex; i < text.length; i++) {
    if (text[i] === "{") { depth++; continue; }
    if (text[i] !== "}") continue;
    if (--depth === 0) return i;
  }
  return -1;
}

function hasFoundationMetadata(body) {
  return /\bfoundation\.v1\.Metadata\s+metadata\s*=\s*1\s*;/.test(body);
}

function actionFromOperation(operation) {
  const snake = lowerSnake(operation);
  for (const action of KNOWN_ACTIONS) {
    if (snake === action || snake.startsWith(`${action}_`)) return action;
  }
  return snake;
}

function lowerSnake(value) {
  return value
    .replace(/([a-z0-9])([A-Z])/g, "$1_$2")
    .replace(/([A-Z]+)([A-Z][a-z])/g, "$1_$2")
    .replace(/[^A-Za-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .toLowerCase();
}

function matchOne(text, regex) {
  const m = regex.exec(text);
  return m ? m[1] : "";
}

// ── Argument parsing ──────────────────────────────────────────────────────────

function parseArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i++) {
    switch (argv[i]) {
      case "--proto-root":    args.protoRoot = requireValue(argv, ++i, "--proto-root"); break;
      case "--manifest-out":  args.manifestOut = requireValue(argv, ++i, "--manifest-out"); break;
      case "--guide-out":     args.guideOut = requireValue(argv, ++i, "--guide-out"); break;
      case "--include-template": args.includeTemplate = true; break;
      case "--check":         args.check = true; break;
      case "--help": case "-h": printHelp(); process.exit(0); break;
      default: throw new Error(`unknown argument: ${argv[i]}`);
    }
  }
  return args;
}

function requireValue(argv, index, flag) {
  const value = argv[index];
  if (!value || value.startsWith("--")) {
    throw new Error(`${flag} requires a value`);
  }
  return value;
}

function printHelp() {
  console.log(`Usage: node generate_lifecycle_manifest.mjs [options]

Options:
  --proto-root <dir>    Proto root. Default: api/protos
  --manifest-out <f>    JSON manifest output. Default: docs/references/lifecycle/lifecycle_contract.json
  --guide-out <f>       Markdown guide output. Default: docs/references/lifecycle/lifecycle_contract_guide.md
  --include-template    Include api/protos/_template fixtures
  --check               CI mode: fail if outputs are missing or stale
  --help                Print this message
`);
}

function stableProtoRootLabel(protoRootArg, protoRoot) {
  const normalized = protoRoot.split(path.sep).join("/");
  if (normalized.endsWith("/templates/api/protos")) {
    return "api/protos";
  }
  if (normalized.endsWith("/api/protos")) {
    return "api/protos";
  }
  if (!path.isAbsolute(protoRootArg)) {
    return protoRootArg.split(path.sep).join("/");
  }
  return (path.relative(process.cwd(), protoRoot) || ".").split(path.sep).join("/");
}

function rel(file) {
  return path.relative(process.cwd(), file) || ".";
}

// ── Entry point ───────────────────────────────────────────────────────────────

try {
  main();
} catch (err) {
  console.error(`[lifecycle-manifest] ${err.message}`);
  process.exit(1);
}

#!/usr/bin/env node
import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";

const DEFAULT_IMPORT_ROOT = "github.com/nmxmxh/ovasabi_foundation/server-kit/go";
const mutatingActions = new Set([
  "accept",
  "activate",
  "add",
  "apply",
  "approve",
  "archive",
  "assign",
  "authenticate",
  "cancel",
  "close",
  "complete",
  "confirm",
  "create",
  "deactivate",
  "delete",
  "disable",
  "enable",
  "execute",
  "generate",
  "import",
  "invite",
  "issue",
  "merge",
  "patch",
  "pause",
  "process",
  "publish",
  "reject",
  "remove",
  "restore",
  "resume",
  "revoke",
  "run",
  "send",
  "set",
  "start",
  "stop",
  "submit",
  "sync",
  "update",
  "upsert",
  "upload",
]);

const readOnlyActions = new Set(["count", "describe", "find", "get", "list", "query", "read", "search", "watch"]);
const knownActions = [...mutatingActions, ...readOnlyActions].sort((a, b) => b.length - a.length);

function main() {
  const args = parseArgs(process.argv.slice(2));
  const protoRootArg = args.protoRoot ?? "api/protos";
  const protoRoot = path.resolve(protoRootArg);
  const protoRootLabel = stableProtoRootLabel(protoRootArg, protoRoot);
  const outPath = path.resolve(args.out ?? "tests/contract/generated_lifecycle_test.go");
  const includeTemplate = Boolean(args.includeTemplate);
  const importRoot = args.importRoot ?? DEFAULT_IMPORT_ROOT;
  const packageName = args.packageName ?? "contract_test";

  const result = discoverContracts(protoRoot, includeTemplate);
  if (result.errors.length > 0) {
    for (const error of result.errors) {
      console.error(`[lifecycle-contracts] ${error}`);
    }
    process.exit(1);
  }

  const content = formatGo(renderGoTest({
    cases: result.cases,
    importRoot,
    packageName,
    protoRootLabel,
  }));

  if (args.check) {
    if (result.cases.length === 0 && !existsSync(outPath)) {
      console.log("[OK] no mutating lifecycle contracts discovered");
      return;
    }
    if (!existsSync(outPath)) {
      console.error(`[FAIL] lifecycle contract test missing: ${relativeToCwd(outPath)}`);
      console.error("  run: make lifecycle-contracts");
      process.exit(1);
    }
    const current = readFileSync(outPath, "utf8");
    if (current !== content) {
      console.error(`[FAIL] lifecycle contract test is stale: ${relativeToCwd(outPath)}`);
      console.error("  run: make lifecycle-contracts");
      process.exit(1);
    }
    console.log(`[OK] lifecycle contract test current: ${relativeToCwd(outPath)}`);
    return;
  }

  mkdirSync(path.dirname(outPath), { recursive: true });
  writeFileSync(outPath, content);
  console.log(`[OK] generated ${result.cases.length} lifecycle contract cases at ${relativeToCwd(outPath)}`);
}

function parseArgs(argv) {
  const args = {};
  for (let i = 0; i < argv.length; i += 1) {
    const arg = argv[i];
    switch (arg) {
      case "--proto-root":
        args.protoRoot = requireValue(argv, ++i, arg);
        break;
      case "--out":
        args.out = requireValue(argv, ++i, arg);
        break;
      case "--import-root":
        args.importRoot = requireValue(argv, ++i, arg);
        break;
      case "--package":
        args.packageName = requireValue(argv, ++i, arg);
        break;
      case "--include-template":
        args.includeTemplate = true;
        break;
      case "--check":
        args.check = true;
        break;
      case "--help":
      case "-h":
        printHelp();
        process.exit(0);
        break;
      default:
        throw new Error(`unknown argument: ${arg}`);
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
  console.log(`Usage: node generate_lifecycle_contract_tests.mjs [options]

Options:
  --proto-root <dir>    Proto root. Defaults to api/protos.
  --out <file>          Generated Go test path. Defaults to tests/contract/generated_lifecycle_test.go.
  --import-root <path>  Foundation server-kit module import root.
  --package <name>      Go package for the generated test. Defaults to contract_test.
  --include-template    Include api/protos/_template fixtures.
  --check               Fail when the generated output is missing or stale.
`);
}

function stableProtoRootLabel(protoRootArg, protoRoot) {
  const normalized = protoRoot.split(path.sep).join("/");
  if (normalized.endsWith("/api/protos")) {
    return "api/protos";
  }
  if (!path.isAbsolute(protoRootArg)) {
    return protoRootArg.split(path.sep).join("/");
  }
  return (path.relative(process.cwd(), protoRoot) || ".").split(path.sep).join("/");
}

function discoverContracts(protoRoot, includeTemplate) {
  const cases = [];
  const errors = [];
  if (!existsSync(protoRoot)) {
    return { cases, errors };
  }

  for (const file of findProtoFiles(protoRoot)) {
    const normalized = file.split(path.sep).join("/");
    if (!includeTemplate && normalized.includes("/_template/")) {
      continue;
    }
    const parsed = parseProtoFile(file, protoRoot);
    errors.push(...parsed.errors);
    cases.push(...parsed.cases);
  }

  cases.sort((a, b) => {
    const left = `${a.domain}:${a.action}:${a.version}:${a.terminal}`;
    const right = `${b.domain}:${b.action}:${b.version}:${b.terminal}`;
    return left.localeCompare(right);
  });
  return { cases, errors };
}

function findProtoFiles(root) {
  const files = [];
  walk(root, files);
  return files.sort();
}

function walk(dir, files) {
  for (const entry of readdirSync(dir).sort()) {
    const fullPath = path.join(dir, entry);
    const stat = statSync(fullPath);
    if (stat.isDirectory()) {
      walk(fullPath, files);
      continue;
    }
    if (entry.endsWith(".proto")) {
      files.push(fullPath);
    }
  }
}

function parseProtoFile(file, protoRoot) {
  const text = readFileSync(file, "utf8");
  const relativeFile = path.relative(protoRoot, file).split(path.sep).join("/");
  const packageName = matchOne(text, /^\s*package\s+([A-Za-z0-9_.]+)\s*;/m);
  const errors = [];
  const cases = [];
  if (!packageName) {
    return { cases, errors };
  }

  const packageParts = packageName.split(".");
  const versionIndex = packageParts.findIndex((part) => /^v[0-9]+$/.test(part));
  if (versionIndex <= 0) {
    return { cases, errors };
  }
  const domain = lowerSnake(packageParts.slice(0, versionIndex).join("_"));
  const version = packageParts[versionIndex];
  if (domain === "common" || domain === "transport") {
    return { cases, errors };
  }

  const messages = parseMessages(text);
  const messageNames = new Set(messages.map((message) => message.name));
  for (const message of messages) {
    if (!message.name.endsWith("Request")) {
      continue;
    }
    const operation = message.name.slice(0, -"Request".length);
    const action = actionFromOperation(operation);
    if (!mutatingActions.has(action)) {
      continue;
    }

    if (!hasRequestMetadata(message.body)) {
      errors.push(`${relativeFile}: ${message.name} must declare common.v1.RequestMetadata metadata = 1`);
      continue;
    }

    const responseName = `${operation}Response`;
    if (!messageNames.has(responseName)) {
      errors.push(`${relativeFile}: ${message.name} must have matching ${responseName}`);
      continue;
    }

    for (const terminal of ["success", "failed"]) {
      cases.push({
        name: `${domain}_${action}_${version}_${terminal}`,
        source: relativeFile,
        request: message.name,
        response: responseName,
        domain,
        action,
        version,
        terminal,
      });
    }
  }
  return { cases, errors };
}

function parseMessages(text) {
  const messages = [];
  const matcher = /\bmessage\s+([A-Za-z0-9_]+)\s*\{/g;
  let match;
  while ((match = matcher.exec(text)) !== null) {
    const name = match[1];
    const bodyStart = matcher.lastIndex;
    const bodyEnd = findMatchingBrace(text, bodyStart - 1);
    if (bodyEnd < 0) {
      continue;
    }
    messages.push({ name, body: text.slice(bodyStart, bodyEnd) });
    matcher.lastIndex = bodyEnd + 1;
  }
  return messages;
}

function findMatchingBrace(text, openIndex) {
  let depth = 0;
  for (let i = openIndex; i < text.length; i += 1) {
    if (text[i] === "{") {
      depth += 1;
      continue;
    }
    if (text[i] !== "}") {
      continue;
    }
    depth -= 1;
    if (depth === 0) {
      return i;
    }
  }
  return -1;
}

function hasRequestMetadata(body) {
  return /\b(?:[A-Za-z0-9_.]+\.)?RequestMetadata\s+metadata\s*=\s*1\s*;/.test(body);
}

function actionFromOperation(operation) {
  const snake = lowerSnake(operation);
  for (const action of knownActions) {
    if (snake === action || snake.startsWith(`${action}_`)) {
      return action;
    }
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
  const match = regex.exec(text);
  return match ? match[1] : "";
}

function renderGoTest({ cases, importRoot, packageName, protoRootLabel }) {
  const renderedCases = cases
    .map((item) => {
      const requested = `${item.domain}:${item.action}:${item.version}:requested`;
      const terminal = `${item.domain}:${item.action}:${item.version}:${item.terminal}`;
      return `\t\t{
\t\t\tname: "${item.name}",
\t\t\trequestMessage: "${item.request}",
\t\t\tresponseMessage: "${item.response}",
\t\t\tsourceProto: "${item.source}",
\t\t\trequestedEventType: "${requested}",
\t\t\tterminalEventType: "${terminal}",
\t\t\tjobKind: "${item.domain}.${item.action}",
\t\t\tqueue: "${item.domain}",
\t\t},`;
    })
    .join("\n");
  const emptyGuard =
    cases.length === 0
      ? `\tif len(cases) == 0 {
\t\tt.Skip("no mutating lifecycle contracts discovered under ${escapeGo(protoRootLabel)}")
\t}
`
      : "";

  return `// Code generated by foundation/tooling/scripts/generate_lifecycle_contract_tests.mjs; DO NOT EDIT.

package ${packageName}

import (
\t"testing"
\t"time"

\t"${importRoot}/contracttest"
\t"${importRoot}/events"
\t"${importRoot}/worker"
)

type generatedLifecycleContractCase struct {
\tname string
\trequestMessage string
\tresponseMessage string
\tsourceProto string
\trequestedEventType string
\tterminalEventType string
\tjobKind string
\tqueue string
}

func TestGeneratedLifecycleContracts(t *testing.T) {
\tcases := generatedLifecycleContractCases()
${emptyGuard}\tfor _, tc := range cases {
\t\tt.Run(tc.name, func(t *testing.T) {
\t\t\tmetadata := generatedLifecycleMetadata()
\t\t\tobs := contracttest.LifecycleObservation{
\t\t\t\tRequested: generatedLifecycleEnvelope(tc.requestedEventType, metadata),
\t\t\t\tTerminal: generatedLifecycleEnvelope(tc.terminalEventType, metadata),
\t\t\t\tJobs: []worker.Job{{
\t\t\t\t\tID: "job_" + tc.name,
\t\t\t\t\tJobKind: tc.jobKind,
\t\t\t\t\tQueue: tc.queue,
\t\t\t\t\tCorrelationID: "corr_generated_contract",
\t\t\t\t\tIdempotencyKey: "idem_generated_contract",
\t\t\t\t\tMaxAttempts: 2,
\t\t\t\t\tMetadata: metadata,
\t\t\t\t\tPayload: map[string]any{
\t\t\t\t\t\t"request_message": tc.requestMessage,
\t\t\t\t\t\t"response_message": tc.responseMessage,
\t\t\t\t\t\t"source_proto": tc.sourceProto,
\t\t\t\t\t},
\t\t\t\t}},
\t\t\t}
\t\t\tif err := contracttest.VerifyCommandLifecycle(obs, contracttest.LifecycleOptions{
\t\t\t\tRequireIdempotency: true,
\t\t\t\tRequireTenant: true,
\t\t\t}); err != nil {
\t\t\t\tt.Fatalf("generated lifecycle contract %s failed: %v", tc.name, err)
\t\t\t}
\t\t})
\t}
}

func generatedLifecycleContractCases() []generatedLifecycleContractCase {
\treturn []generatedLifecycleContractCase{
${renderedCases}
\t}
}

func verifyGeneratedLifecycleObservation(t *testing.T, name string, obs contracttest.LifecycleObservation) {
\tt.Helper()
\ttc, ok := generatedLifecycleContractByName(name)
\tif !ok {
\t\tt.Fatalf("unknown generated lifecycle contract %q", name)
\t}
\tif obs.Requested.EventType != tc.requestedEventType {
\t\tt.Fatalf("requested event type = %q, want %q", obs.Requested.EventType, tc.requestedEventType)
\t}
\tif obs.Terminal.EventType != tc.terminalEventType {
\t\tt.Fatalf("terminal event type = %q, want %q", obs.Terminal.EventType, tc.terminalEventType)
\t}
\tif err := contracttest.VerifyCommandLifecycle(obs, contracttest.LifecycleOptions{
\t\tRequireIdempotency: true,
\t\tRequireTenant: true,
\t}); err != nil {
\t\tt.Fatalf("observed lifecycle contract %s failed: %v", name, err)
\t}
}

func generatedLifecycleContractByName(name string) (generatedLifecycleContractCase, bool) {
\tfor _, tc := range generatedLifecycleContractCases() {
\t\tif tc.name == name {
\t\t\treturn tc, true
\t\t}
\t}
\treturn generatedLifecycleContractCase{}, false
}

func generatedLifecycleEnvelope(eventType string, metadata map[string]any) events.Envelope {
\tenv := events.Envelope{
\t\tEventType: eventType,
\t\tPayload: map[string]any{"source": "generated_lifecycle_contract"},
\t\tMetadata: generatedLifecycleCopy(metadata),
\t\tCorrelationID: "corr_generated_contract",
\t\tSchemaVersion: events.EnvelopeSchemaVersion,
\t\tTimestamp: time.Unix(1700000000, 0).UTC(),
\t}
\tenv.Normalize()
\treturn env
}

func generatedLifecycleMetadata() map[string]any {
\treturn map[string]any{
\t\t"correlation_id": "corr_generated_contract",
\t\t"idempotency_key": "idem_generated_contract",
\t\t"organization_id": "org_generated_contract",
\t}
}

func generatedLifecycleCopy(in map[string]any) map[string]any {
\tout := make(map[string]any, len(in))
\tfor key, value := range in {
\t\tout[key] = value
\t}
\treturn out
}
`;
}

function formatGo(source) {
  const result = spawnSync("gofmt", [], {
    input: source,
    encoding: "utf8",
  });
  if (result.error) {
    throw new Error(`gofmt failed: ${result.error.message}`);
  }
  if (result.status !== 0) {
    throw new Error(`gofmt failed: ${result.stderr.trim()}`);
  }
  return result.stdout;
}

function escapeGo(value) {
  return value.replace(/\\/g, "\\\\").replace(/"/g, '\\"');
}

function relativeToCwd(file) {
  return path.relative(process.cwd(), file) || ".";
}

try {
  main();
} catch (error) {
  console.error(`[lifecycle-contracts] ${error.message}`);
  process.exit(1);
}

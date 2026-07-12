#!/usr/bin/env node
import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import process from "node:process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const graphPath = path.join(root, "tooling/agent_architecture_graph.json");
const args = process.argv.slice(2);
const command = args.shift();

const option = (name, fallback = "") => {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] ?? fallback : fallback;
};
const has = (name) => args.includes(name);
const readJSON = (file) => JSON.parse(readFileSync(path.resolve(file), "utf8"));
const writeJSON = (file, value) => {
  const target = path.resolve(file);
  mkdirSync(path.dirname(target), { recursive: true });
  writeFileSync(target, `${JSON.stringify(value, null, 2)}\n`);
  return target;
};

const classifyRisk = (feature, commands) => {
  const value = `${feature} ${commands.join(" ")}`;
  if (/(auth|policy|money|payment|ledger|tenant|schema|crypto|wasm-memory)/.test(value)) return "tier1";
  if (/(style|copy|theme|layout|content)/.test(value)) return "tier3";
  return "tier2";
};

const validate = (model) => {
  const errors = [];
  if (model.schemaVersion !== "1.0") errors.push("schemaVersion must be 1.0");
  if (!model.objective) errors.push("objective is required");
  if (!/^[a-z][a-z0-9-]*$/.test(model.feature ?? "")) errors.push("feature must be kebab-case");
  if (!Array.isArray(model.commands) || model.commands.length === 0) errors.push("at least one command is required");
  for (const item of model.commands ?? []) {
    if (!item.requested?.endsWith(":requested") || !item.success?.endsWith(":success") || !item.failed?.endsWith(":failed")) {
      errors.push(`command ${item.action ?? "unknown"} requires requested/success/failed terminals`);
    }
  }
  if (!model.worker?.bounded || !(model.worker.maxAttempts > 0)) errors.push("worker must be bounded with positive maxAttempts");
  if (!model.fallback) errors.push("fallback is required");
  if (!model.conflictPolicy) errors.push("conflictPolicy is required");
  if (model.realtime && !model.projection) errors.push("realtime changes require a projection");
  if (model.risk === "tier1" && !model.requiredEvidence?.includes("human-approval")) errors.push("tier1 requires human-approval evidence");
  return errors;
};

if (command === "graph") {
  const graph = readJSON(graphPath);
  const capability = option("--capability");
  const output = capability ? graph.capabilities.find((item) => item.capability === capability) : graph;
  if (!output) throw new Error(`unknown capability: ${capability}`);
  process.stdout.write(`${JSON.stringify(output, null, 2)}\n`);
} else if (command === "plan") {
  const feature = option("--feature");
  const actions = option("--commands").split(",").map((value) => value.trim()).filter(Boolean);
  const projection = option("--projection") || null;
  const risk = classifyRisk(feature, actions);
  const requiredEvidence = risk === "tier1"
    ? ["unit", "integration", "contract", "security-review", "human-approval"]
    : risk === "tier2" ? ["unit", "contract", "coverage"] : ["lint", "visual-review"];
  const model = {
    schemaVersion: "1.0",
    objective: option("--objective", `Add ${feature} capability`),
    feature,
    risk,
    approval: risk === "tier1" ? "required" : risk === "tier2" ? "review" : "autonomous",
    commands: actions.map((action) => ({
      action,
      requested: `${feature}:${action}:v1:requested`,
      success: `${feature}:${action}:v1:success`,
      failed: `${feature}:${action}:v1:failed`,
    })),
    projection,
    worker: { bounded: true, maxAttempts: Number(option("--max-attempts", "5")) },
    offline: has("--offline"),
    realtime: has("--realtime"),
    conflictPolicy: option("--conflict-policy", "server-version-wins"),
    fallback: option("--fallback", has("--realtime") ? "HTTP after WebSocket loss" : "controlled error"),
    invariants: ["MetadataPreserved", "TenantScopeStable", "ExactlyOneTerminalVisible", "IdempotentRetry", "BoundedWork", "FallbackRefinement"],
    requiredEvidence,
  };
  const errors = validate(model);
  if (errors.length) throw new Error(errors.join("; "));
  const out = option("--out", path.join(option("--project-dir", process.cwd()), ".foundation/changes", `${feature}.json`));
  process.stdout.write(`[OK] change model: ${writeJSON(out, model)}\n`);
} else if (command === "check") {
  const file = option("--file");
  const errors = validate(readJSON(file));
  if (errors.length) throw new Error(errors.join("; "));
  process.stdout.write(`[OK] agent change model valid: ${path.resolve(file)}\n`);
} else if (command === "evidence") {
  const plan = readJSON(option("--plan"));
  const errors = validate(plan);
  if (errors.length) throw new Error(errors.join("; "));
  const evidence = {
    schemaVersion: "1.0",
    objective: plan.objective,
    risk: plan.risk,
    filesTouched: [],
    contractsChanged: plan.commands.map((item) => item.requested),
    invariants: plan.invariants,
    commandsRun: [],
    coverageBefore: {},
    coverageAfter: {},
    benchmarks: {},
    fallback: plan.fallback,
    knownGaps: [],
    requiredEvidence: plan.requiredEvidence,
  };
  const out = option("--out", path.join(path.dirname(path.resolve(option("--plan"))), `${plan.feature}.evidence.json`));
  process.stdout.write(`[OK] evidence ledger: ${writeJSON(out, evidence)}\n`);
} else {
  throw new Error("usage: agent_change.mjs graph|plan|check|evidence [options]");
}

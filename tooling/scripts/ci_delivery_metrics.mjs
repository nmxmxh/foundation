#!/usr/bin/env node

import { mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";

const args = process.argv.slice(2);
const outIndex = args.indexOf("--out");
const outPath = resolve(outIndex >= 0 && args[outIndex + 1] ? args[outIndex + 1] : "delivery-metrics/ci-event.json");

const env = process.env;
const now = new Date();
const payload = readEventPayload(env.GITHUB_EVENT_PATH);
const eventName = env.GITHUB_EVENT_NAME || "local";
const commitTime = readCommitTime(payload);
const incidentStartedAt = parseDate(env.INCIDENT_STARTED_AT);
const incidentResolvedAt = parseDate(env.INCIDENT_RESOLVED_AT);
const runAttempt = Number.parseInt(env.GITHUB_RUN_ATTEMPT || "1", 10);

const record = {
  schema_version: "foundation.delivery_metrics.v1",
  collected_at: now.toISOString(),
  provider: env.GITHUB_ACTIONS === "true" ? "github_actions" : "local",
  repository: env.GITHUB_REPOSITORY || "",
  workflow: env.GITHUB_WORKFLOW || "",
  run_id: env.GITHUB_RUN_ID || "",
  run_attempt: Number.isFinite(runAttempt) ? runAttempt : 1,
  event_name: eventName,
  ref: env.GITHUB_REF || "",
  ref_name: env.GITHUB_REF_NAME || "",
  sha: env.GITHUB_SHA || "",
  actor: env.GITHUB_ACTOR || "",
  dora: {
    change_lead_time_seconds: secondsBetween(commitTime, now),
    deployment_frequency_signal: isDeploymentSignal(eventName, env.GITHUB_REF_NAME || env.GITHUB_REF || ""),
    change_failure_signal: env.FOUNDATION_CHANGE_FAILED === "true",
    failed_deployment_recovery_time_seconds: secondsBetween(incidentStartedAt, incidentResolvedAt || now),
    deployment_rework_signal: (Number.isFinite(runAttempt) ? runAttempt : 1) > 1 || env.FOUNDATION_DEPLOYMENT_REWORK === "true",
  },
  incident: {
    id: env.INCIDENT_ID || "",
    severity: env.INCIDENT_SEVERITY || "",
    started_at: incidentStartedAt ? incidentStartedAt.toISOString() : "",
    resolved_at: incidentResolvedAt ? incidentResolvedAt.toISOString() : "",
    service: env.INCIDENT_SERVICE || "",
  },
};

mkdirSync(dirname(outPath), { recursive: true });
writeFileSync(outPath, `${JSON.stringify(record, null, 2)}\n`);
console.log(`wrote ${outPath}`);

function readEventPayload(path) {
  if (!path) {
    return {};
  }
  try {
    return JSON.parse(readFileSync(path, "utf8"));
  } catch {
    return {};
  }
}

function readCommitTime(payload) {
  const candidates = [
    payload?.head_commit?.timestamp,
    payload?.pull_request?.merged_at,
    payload?.pull_request?.created_at,
    payload?.deployment?.created_at,
    payload?.workflow_run?.created_at,
  ];
  for (const candidate of candidates) {
    const parsed = parseDate(candidate);
    if (parsed) {
      return parsed;
    }
  }
  return undefined;
}

function parseDate(value) {
  if (!value) {
    return undefined;
  }
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return undefined;
  }
  return parsed;
}

function secondsBetween(start, end) {
  if (!start || !end) {
    return null;
  }
  return Math.max(0, Math.round((end.getTime() - start.getTime()) / 1000));
}

function isDeploymentSignal(eventName, refName) {
  if (eventName === "deployment" || eventName === "deployment_status") {
    return true;
  }
  return eventName === "push" && (refName === "main" || refName.endsWith("/main"));
}

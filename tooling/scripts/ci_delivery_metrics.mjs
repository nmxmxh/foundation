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
const incidentDetectedAt = parseDate(env.INCIDENT_DETECTED_AT);
const incidentMitigatedAt = parseDate(env.INCIDENT_MITIGATED_AT);
const incidentResolvedAt = parseDate(env.INCIDENT_RESOLVED_AT);
const runAttempt = Number.parseInt(env.GITHUB_RUN_ATTEMPT || "1", 10);
const commitCount = Array.isArray(payload?.commits) ? payload.commits.length : null;

const record = {
  schema_version: "foundation.delivery_metrics.v2",
  schema_compatibility: ["foundation.delivery_metrics.v1"],
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
  space: {
    satisfaction_signal: parseNumber(env.FOUNDATION_DEVEX_SATISFACTION_SCORE),
    performance_signal: env.FOUNDATION_PERFORMANCE_REGRESSION === "true" ? "regression" : env.FOUNDATION_PERFORMANCE_SIGNAL || "",
    activity_commit_count: commitCount,
    communication_review_latency_seconds: parseNumber(env.FOUNDATION_REVIEW_LATENCY_SECONDS),
    efficiency_local_setup_seconds: parseNumber(env.FOUNDATION_LOCAL_SETUP_SECONDS),
    efficiency_flaky_test_signal: env.FOUNDATION_FLAKY_TEST === "true",
    efficiency_rework_signal: env.FOUNDATION_REWORK === "true" || (Number.isFinite(runAttempt) ? runAttempt : 1) > 1,
  },
  observability: {
    otel_semconv_version: env.OTEL_SEMCONV_VERSION || "",
    trace_id: env.OTEL_TRACE_ID || env.TRACE_ID || "",
    span_id: env.OTEL_SPAN_ID || env.SPAN_ID || "",
    service_name: env.OTEL_SERVICE_NAME || env.INCIDENT_SERVICE || "",
    correlation_id: env.CORRELATION_ID || "",
  },
  supply_chain: {
    slsa_provenance_path: env.FOUNDATION_SLSA_PROVENANCE_PATH || "",
    sbom_path: env.FOUNDATION_SBOM_PATH || "sbom.spdx.json",
    sbom_format: env.FOUNDATION_SBOM_FORMAT || "spdx-json",
    artifact_digest: env.FOUNDATION_ARTIFACT_DIGEST || "",
    builder_id: env.GITHUB_WORKFLOW_REF || env.FOUNDATION_BUILDER_ID || "",
    source_ref: env.GITHUB_REF || "",
    source_sha: env.GITHUB_SHA || "",
  },
  incident: {
    id: env.INCIDENT_ID || "",
    severity: env.INCIDENT_SEVERITY || "",
    started_at: incidentStartedAt ? incidentStartedAt.toISOString() : "",
    detected_at: incidentDetectedAt ? incidentDetectedAt.toISOString() : "",
    mitigated_at: incidentMitigatedAt ? incidentMitigatedAt.toISOString() : "",
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

function parseNumber(value) {
  if (value === undefined || value === null || value === "") {
    return null;
  }
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) {
    return null;
  }
  return parsed;
}

function isDeploymentSignal(eventName, refName) {
  if (eventName === "deployment" || eventName === "deployment_status") {
    return true;
  }
  return eventName === "push" && (refName === "main" || refName.endsWith("/main"));
}

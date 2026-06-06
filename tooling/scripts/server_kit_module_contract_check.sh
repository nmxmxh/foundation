#!/bin/bash
set -euo pipefail

target="${1:-.}"
kit="$target/server-kit/go"
failed=0
tmp_output="${TMPDIR:-/tmp}/ovasabi_server_kit_module_contract.out"

if [[ -d "$target/foundation/server-kit/go" ]]; then
  kit="$target/foundation/server-kit/go"
fi

fail() {
  echo "[FAIL] $1"
  shift
  if [[ "$#" -gt 0 ]]; then
    printf '%s\n' "$@" | sed 's/^/  /'
  fi
  failed=1
}

ok() {
  echo "[OK] $1"
}

run_with_timeout() {
  local timeout_sec="$1"
  shift
  if command -v perl >/dev/null 2>&1; then
    perl -e '
      use strict;
      use warnings;
      use POSIX ":sys_wait_h";

      my $timeout = shift @ARGV;
      my @cmd = @ARGV;
      my $pid = fork();
      die "fork failed: $!\n" unless defined $pid;
      if ($pid == 0) {
        setpgrp(0, 0) or die "setpgrp failed: $!\n";
        exec @cmd or die "exec failed: $!\n";
      }
      my $deadline = time() + $timeout;
      while (1) {
        my $done = waitpid($pid, WNOHANG);
        if ($done == $pid) {
          my $status = $?;
          if ($status & 127) {
            exit 128 + ($status & 127);
          }
          exit($status >> 8);
        }
        if (time() >= $deadline) {
          kill "TERM", -$pid;
          select(undef, undef, undef, 0.5);
          kill "KILL", -$pid;
          waitpid($pid, 0);
          print STDERR "command timed out after ${timeout}s: @cmd\n";
          exit 124;
        }
        select(undef, undef, undef, 0.2);
      }
    ' "$timeout_sec" "$@"
  else
    "$@"
  fi
}

check_exists() {
  local label="$1"
  local path="$2"
  if [[ -e "$path" ]]; then
    ok "$label"
  else
    fail "$label" "missing: ${path#$target/}"
  fi
}

check_no_match() {
  local label="$1"
  local pattern="$2"
  shift 2
  echo "[RUN] $label"
  if run_with_timeout "${SERVER_KIT_CONTRACT_RG_TIMEOUT_SEC:-30}" rg --no-config -n "$pattern" "$@" >"$tmp_output" 2>/dev/null; then
    fail "$label" "$(cat "$tmp_output")"
  else
    local exit_code=$?
    if [[ "$exit_code" -eq 1 ]]; then
      ok "$label"
    else
      fail "$label" "$(cat "$tmp_output" 2>/dev/null || true)"
    fi
  fi
}

check_file_contains() {
  local label="$1"
  local file="$2"
  local pattern="$3"
  if [[ -f "$file" ]] && grep -Fq -- "$pattern" "$file"; then
    ok "$label"
  else
    fail "$label" "missing pattern: $pattern" "file: ${file#$target/}"
  fi
}

check_module() {
  local name="$1"
  local required_file="$2"
  local required_test="$3"
  check_exists "server-kit module: $name" "$kit/$required_file"
  check_exists "server-kit module tests: $name" "$kit/$required_test"
}

check_exists "server-kit go.mod" "$kit/go.mod"

check_module "auth" "auth/jwt.go" "auth/jwt_test.go"
check_module "bootstrap" "bootstrap/bootstrap.go" "bootstrap/bootstrap_test.go"
check_module "bulk" "bulk/manager.go" "bulk/manager_test.go"
check_module "cache" "cache/cache.go" "cache/cache_test.go"
check_module "circuitbreaker" "circuitbreaker/circuitbreaker.go" "circuitbreaker/circuitbreaker_test.go"
check_module "compress" "compress/compress.go" "compress/compress_test.go"
check_module "contracttest" "contracttest/lifecycle.go" "contracttest/lifecycle_test.go"
check_module "database" "database/executor.go" "database/executor_test.go"
check_module "domainerr" "domainerr/errors.go" "domainerr/errors_test.go"
check_module "eventlog" "eventlog/eventlog.go" "eventlog/eventlog_test.go"
check_module "events" "events/envelope.go" "events/envelope_binary_test.go"
check_module "graceful" "graceful/graceful.go" "graceful/graceful_test.go"
check_module "grpcsvc" "grpcsvc/grpcsvc.go" "grpcsvc/grpcsvc_test.go"
check_module "healthcheck" "healthcheck/healthcheck.go" "healthcheck/healthcheck_test.go"
check_module "hermes" "hermes/store.go" "hermes/store_test.go"
check_module "httpapi" "httpapi/dispatch_route.go" "httpapi/dispatch_route_test.go"
check_module "intelligence" "intelligence/intelligence.go" "intelligence/intelligence_test.go"
check_module "logger" "logger/logger.go" "logger/logger_test.go"
check_module "metadata" "metadata/metadata.go" "metadata/metadata_test.go"
check_module "objectstore" "objectstore/object_store.go" "objectstore/object_store_test.go"
check_module "observability" "observability/collector.go" "observability/collector_test.go"
check_module "policy" "policy/policy.go" "policy/policy_test.go"
check_module "protoapi" "protoapi/binding.go" "protoapi/binding_test.go"
check_module "redis" "redis/client.go" "redis/client_test.go"
check_module "registry" "registry/registry.go" "registry/registry_test.go"
check_module "resilience" "resilience/resilience.go" "resilience/resilience_test.go"
check_module "retry" "retry/retry.go" "retry/retry_test.go"
check_module "security" "security/middleware.go" "security/middleware_test.go"
check_module "slo" "slo/slo.go" "slo/slo_test.go"
check_module "startup" "startup/runtime.go" "startup/runtime_test.go"
check_module "worker" "worker/engine.go" "worker/engine_test.go"
check_module "wsmetrics" "wsmetrics/wsmetrics.go" "wsmetrics/wsmetrics_test.go"
check_module "wsrouting" "wsrouting/wsrouting.go" "wsrouting/wsrouting_test.go"

check_file_contains "database exposes atomic executor lane" "$kit/database/executor.go" "AtomicLane"
check_file_contains "database exposes pool lane budgets" "$kit/database/database.go" "DefaultPoolOptionsFor"
check_file_contains "redis exposes batch client" "$kit/redis/client.go" "SetGetMany"
check_file_contains "redis exposes stream batch append client" "$kit/redis/client.go" "XAddMany"
check_file_contains "metadata exposes context map merge" "$kit/metadata/metadata.go" "FromContextMap"
check_file_contains "logger exposes Foundation logger interface" "$kit/logger/logger.go" "type Logger interface"
check_file_contains "intelligence observer is non-blocking" "$kit/intelligence/intelligence.go" "ObserveIntelligence(context.Background()"
check_file_contains "Hermes wraps runtime store" "$kit/hermes/state_store.go" "ProjectedRuntimeStore"
check_file_contains "Hermes exposes trusted snapshot bulk load" "$kit/hermes/store.go" "func (s *Store) BulkLoad"
check_file_contains "eventlog has append-only contract" "$kit/eventlog/eventlog.go" "Append"
check_file_contains "eventlog batches pending stream publication" "$kit/eventlog/eventlog.go" "publishPendingBatch"
check_file_contains "eventlog claims pending publication leases" "$kit/eventlog/eventlog.go" "FOR UPDATE SKIP LOCKED"
check_file_contains "eventlog detects lost publish claims" "$kit/eventlog/eventlog.go" "ErrPublishClaimLost"

check_no_match "server-kit avoids raw process exits in library code" "\\bos\\.Exit\\s*\\(" "$kit" --glob '*.go' --glob '!**/*test.go'
check_no_match "server-kit avoids stdlib log package outside logger module" "\"log\"|\"log/slog\"" "$kit" --glob '*.go' --glob '!**/logger/**' --glob '!**/*test.go'
check_no_match "server-kit avoids fmt.Print diagnostics in library code" "fmt\\.Print(f|ln)?\\s*\\(" "$kit" --glob '*.go' --glob '!**/*test.go'
check_no_match "Hermes byte estimator avoids formatting values" "fmt\\.Sprintf" "$kit/hermes/indexes.go"
check_no_match "server-kit avoids deprecated grpc dialing APIs" "grpc\\.Dial(Context)?\\s*\\(|grpc\\.WithBlock\\s*\\(" "$kit" --glob '*.go' --glob '!**/*test.go'
check_no_match "server-kit avoids unmanaged goroutine launch in request/router modules" "\\bgo\\s+func\\s*\\(" "$kit/httpapi" "$kit/graceful" "$kit/bootstrap" --glob '*.go' --glob '!**/*test.go'

if [[ "$failed" -ne 0 ]]; then
  echo "server-kit module contract check failed"
  exit 1
fi

echo "server-kit module contract check passed"

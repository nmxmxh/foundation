#!/usr/bin/env python3
"""Deep Resource Profiler: Batched, Non-Mutating Index & Memory Audit.

Foundation standard agent diagnostic kit for zero-footprint fleet audits:
  1. Index Utilization: Unused indexes, seq vs index scan ratios, index bloat.
  2. Memory & Cache: Buffer cache hit ratio, index hit ratio, temp file spills.
  3. Dead Tuples: Top bloat leaders, live vs dead tuple ratios.
  4. River Queue Health: State distribution, queue backlog, retention bloat.

Safety Guarantee:
  - All queries use explicit read-only transactions and a 5-second statement timeout.
  - Queries use normal read locks with a 1-second lock timeout.
  - Standard library only: no external pip dependencies required.

Usage:
  ./deep_resource_profiler.py [--json] [--limit 10]
  DATABASE_URL="postgres://user:pass@localhost:5432/dbname" ./deep_resource_profiler.py
  DB_CONTAINER=app-postgres-1 ./deep_resource_profiler.py
"""

from __future__ import annotations

import argparse
import csv
import io
import json
import os
import shlex
import shutil
import subprocess
import sys
from typing import Any, Dict, List


def run_query(sql: str, opts: argparse.Namespace) -> List[Dict[str, str]]:
    """Execute a query in a bounded, read-only transaction and parse CSV output."""
    wrapped_sql = (
        "BEGIN READ ONLY;\n"
        "SET LOCAL statement_timeout = '5s';\n"
        "SET LOCAL lock_timeout = '1s';\n"
        f"{sql}\nCOMMIT;\n"
    )

    cmd: List[str] = []
    env = os.environ.copy()
    env["PGCONNECT_TIMEOUT"] = "5"
    psql_args = ["psql", "-X", "-q", "-w", "--csv", "-v", "ON_ERROR_STOP=1"]

    if opts.ssh_target:
        ssh_cmd = [
            "ssh",
            "-o", "BatchMode=yes",
            "-o", "ConnectTimeout=10",
            "-o", "StrictHostKeyChecking=accept-new",
            "-p", str(opts.ssh_port),
        ]
        remote_args = psql_args + ["-U", opts.user, "-d", opts.database]
        if opts.container:
            remote_args = ["docker", "exec", "-i", "-e", "PGCONNECT_TIMEOUT=5", opts.container] + remote_args
        else:
            remote_args = ["env", "PGCONNECT_TIMEOUT=5"] + remote_args
        ssh_cmd.extend(["--", opts.ssh_target, shlex.join(remote_args)])
        cmd = ssh_cmd
    elif opts.container:
        cmd = [
            "docker", "exec", "-i", "-e", "PGCONNECT_TIMEOUT=5", opts.container,
        ] + psql_args + ["-U", opts.user, "-d", opts.database]
    else:
        psql_path = shutil.which("psql")
        if not psql_path:
            raise RuntimeError("psql client binary not found in PATH")
        cmd = [psql_path] + psql_args[1:]
        if opts.dsn:
            cmd.extend(["-d", opts.dsn])
        else:
            if opts.host:
                cmd.extend(["-h", opts.host])
            if opts.port:
                cmd.extend(["-p", str(opts.port)])
            if opts.user:
                cmd.extend(["-U", opts.user])
            if opts.database:
                cmd.extend(["-d", opts.database])

    try:
        res = subprocess.run(cmd, input=wrapped_sql, env=env, capture_output=True, text=True, timeout=15)
    except subprocess.TimeoutExpired:
        raise RuntimeError("Query execution exceeded the 15-second deadline") from None
    if res.returncode != 0:
        raise RuntimeError(f"Query execution failed (exit {res.returncode}); inspect database logs")

    if not res.stdout.strip():
        return []
    reader = csv.DictReader(io.StringIO(res.stdout), strict=True)
    rows = list(reader)
    if any(None in row or None in row.values() for row in rows):
        raise RuntimeError("Query returned malformed CSV output")
    return rows


def audit_cache_and_memory(opts: argparse.Namespace) -> Dict[str, Any]:
    sql = """
    SELECT
        d.datname,
        ROUND((sum(s.heap_blks_hit) * 100.0 / NULLIF(sum(s.heap_blks_hit) + sum(s.heap_blks_read), 0)), 2) AS heap_hit_ratio,
        ROUND((sum(s.idx_blks_hit) * 100.0 / NULLIF(sum(s.idx_blks_hit) + sum(s.idx_blks_read), 0)), 2) AS idx_hit_ratio,
        d.stats_reset,
        d.temp_files,
        pg_size_pretty(d.temp_bytes) AS temp_size
    FROM pg_stat_database d
    LEFT JOIN pg_statio_user_tables s ON true
    WHERE d.datname = current_database()
    GROUP BY d.datname, d.temp_files, d.temp_bytes, d.stats_reset;
    """
    rows = run_query(sql, opts)
    if rows:
        return rows[0]
    return {}


def audit_scan_efficiency(opts: argparse.Namespace) -> List[Dict[str, Any]]:
    sql = f"""
    SELECT
        relname AS table_name,
        seq_scan,
        seq_tup_read,
        idx_scan,
        idx_tup_fetch,
        CASE
            WHEN (seq_scan + idx_scan) = 0 THEN 0.0
            ELSE ROUND((idx_scan * 100.0 / (seq_scan + idx_scan)), 2)
        END AS idx_scan_pct,
        n_live_tup,
        n_dead_tup
    FROM pg_stat_user_tables
    ORDER BY seq_tup_read DESC
    LIMIT {opts.limit};
    """
    return run_query(sql, opts)


def audit_unused_indexes(opts: argparse.Namespace) -> List[Dict[str, Any]]:
    sql = f"""
    SELECT
        s.schemaname || '.' || s.relname AS table_name,
        s.indexrelname AS index_name,
        s.idx_scan,
        pg_size_pretty(pg_relation_size(s.indexrelid)) AS index_size
    FROM pg_stat_user_indexes s
    JOIN pg_index i ON s.indexrelid = i.indexrelid
    JOIN pg_stat_user_tables t ON s.relid = t.relid
    WHERE s.idx_scan = 0
      AND NOT i.indisunique
      AND NOT i.indisprimary
      AND t.n_live_tup > 1000
    ORDER BY pg_relation_size(s.indexrelid) DESC
    LIMIT {opts.limit};
    """
    return run_query(sql, opts)


def audit_river_queue(opts: argparse.Namespace) -> Dict[str, Any]:
    # Check if river_job exists
    check_sql = "SELECT to_regclass('river_job') AS table_exists;"
    check = run_query(check_sql, opts)
    if not check or not check[0].get("table_exists"):
        return {"status": "river_job table not present"}

    state_sql = """
    SELECT state, count(*) AS count
    FROM river_job
    GROUP BY state
    ORDER BY count DESC;
    """
    states = run_query(state_sql, opts)

    backlog_sql = f"""
    SELECT queue, count(*) AS count
    FROM river_job
    WHERE state = 'available'
    GROUP BY queue
    ORDER BY count DESC
    LIMIT {opts.limit};
    """
    backlog = run_query(backlog_sql, opts)

    age_sql = """
    SELECT COALESCE(max(NOW() - created_at)::text, '0s') AS oldest_available
    FROM river_job
    WHERE state = 'available';
    """
    age = run_query(age_sql, opts)

    # Check for prohibited GIN indexes on river_job
    gin_sql = """
    SELECT indexrelname
    FROM pg_stat_user_indexes
    WHERE relname = 'river_job'
      AND pg_get_indexdef(indexrelid) ILIKE '%USING gin%';
    """
    gin_indexes = run_query(gin_sql, opts)

    return {
        "states": states,
        "backlog": backlog,
        "oldest_available": age[0]["oldest_available"] if age else "0s",
        "prohibited_gin_indexes": gin_indexes,
    }


def format_table(headers: List[str], rows: List[Dict[str, Any]]) -> str:
    if not rows:
        return "  (none)\n"
    col_widths = {h: len(h) for h in headers}
    for row in rows:
        for h in headers:
            col_widths[h] = max(col_widths[h], len(str(row.get(h, ""))))

    header_line = " | ".join(h.ljust(col_widths[h]) for h in headers)
    sep_line = "-+-".join("-" * col_widths[h] for h in headers)
    data_lines = [
        " | ".join(str(row.get(h, "")).ljust(col_widths[h]) for h in headers)
        for row in rows
    ]
    return f"{header_line}\n{sep_line}\n" + "\n".join(data_lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Batched non-mutating PostgreSQL resource and index profiler"
    )
    parser.add_argument("--dsn", default=os.getenv("DATABASE_URL", ""))
    parser.add_argument("--host", default=os.getenv("PGHOST", "localhost"))
    parser.add_argument("--port", type=int, default=int(os.getenv("PGPORT", "5432")))
    parser.add_argument("--user", default=os.getenv("PGUSER", "postgres"))
    parser.add_argument("--database", default=os.getenv("PGDATABASE", "postgres"))
    parser.add_argument("--container", default=os.getenv("DB_CONTAINER", ""))
    parser.add_argument("--ssh-target", default=os.getenv("DB_SSH_TARGET", ""))
    parser.add_argument("--ssh-port", type=int, default=int(os.getenv("DB_SSH_PORT", "22")))
    parser.add_argument("--limit", type=int, default=int(os.getenv("LIMIT", "10")))
    parser.add_argument("--json", action="store_true", help="Output JSON results")

    opts = parser.parse_args()
    if not 1 <= opts.limit <= 1000:
        parser.error("--limit must be between 1 and 1000")

    try:
        cache_stats = audit_cache_and_memory(opts)
        scan_stats = audit_scan_efficiency(opts)
        unused_indexes = audit_unused_indexes(opts)
        river_stats = audit_river_queue(opts)
    except Exception as e:
        print(f"Error executing profile audit: {e}", file=sys.stderr)
        return 1

    report = {
        "cache_and_memory": cache_stats,
        "scan_efficiency": scan_stats,
        "unused_indexes": unused_indexes,
        "river_queue": river_stats,
    }

    if opts.json:
        print(json.dumps(report, indent=2))
        return 0

    print("================================================================================")
    print("FOUNDATION ZERO-FOOTPRINT RESOURCE & INDEX AUDIT")
    print("================================================================================")

    print("\n--- 1. Memory, Cache & Temp Spills ---")
    if cache_stats:
        print(f"  Heap Buffer Cache Hit Ratio: {cache_stats.get('heap_hit_ratio') or 'N/A'}%")
        print(f"  Index Buffer Cache Hit Ratio: {cache_stats.get('idx_hit_ratio') or 'N/A'}%")
        print(f"  Statistics Reset: {cache_stats.get('stats_reset') or 'unknown'}")
        print(f"  Temp Files Created: {cache_stats.get('temp_files', '0')} ({cache_stats.get('temp_size', '0 bytes')})")
        heap_hit = cache_stats.get("heap_hit_ratio")
        if heap_hit and float(heap_hit) < 98.0:
            print("  [WARNING] Heap hit ratio < 98%. Review effective_cache_size and shared_buffers.")
    else:
        print("  (unable to retrieve cache statistics)")

    print("\n--- 2. Scan Efficiency (Top by Sequential Read Volume) ---")
    headers_scan = ["table_name", "seq_scan", "seq_tup_read", "idx_scan", "idx_tup_fetch", "idx_scan_pct", "n_dead_tup"]
    print(format_table(headers_scan, scan_stats))

    print("--- 3. Unused Indexes (>1000 live rows, zero scans) ---")
    headers_unused = ["table_name", "index_name", "idx_scan", "index_size"]
    print(format_table(headers_unused, unused_indexes))

    print("--- 4. River Queue Health & Retention Bloat ---")
    if "status" in river_stats:
        print(f"  {river_stats['status']}")
    else:
        print(f"  Oldest Available Job Age: {river_stats.get('oldest_available', '0s')}")
        print("\n  State Distribution:")
        print(format_table(["state", "count"], river_stats.get("states", [])))

        gin_idxs = river_stats.get("prohibited_gin_indexes", [])
        if gin_idxs:
            print(f"  [CRITICAL ALARM] Prohibited GIN indexes found on river_job: {gin_idxs}")
            print("  Review consumer predicates, query plans, and write costs before any index removal.")
        else:
            print("  [OK] No prohibited GIN indexes on river_job.")

        completed_count = 0
        for s in river_stats.get("states", []):
            if s.get("state") == "completed":
                completed_count = int(s.get("count", 0))
        if completed_count > 10000:
            print(f"  [WARNING] High completed job backlog ({completed_count} rows). Verify tier retention (2-4h).")
        else:
            print(f"  [OK] Completed jobs bounded ({completed_count} rows).")

    print("================================================================================")
    return 0


if __name__ == "__main__":
    sys.exit(main())

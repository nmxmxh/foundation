"""Verify that managed patches respect configuration and logger variations."""

from pathlib import Path
import re
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SOURCE = (ROOT / 'tooling/scripts/scaffold_managed_patches.sh').read_text()


def function(name):
    start = SOURCE.index(name + '() {')
    tail = SOURCE[start:]
    boundary = re.search(r'^\w+\(\) \{|^patch_\w+\s*$', tail[len(name) + 5:], re.M)
    return tail if boundary is None else tail[:len(name) + 5 + boundary.start()]


def execute(root, name):
    program = 'set -euo pipefail\ntarget="$1"\nlog_patch() { :; }\n'
    program += function('replace_in_file') + '\n' + function(name) + '\n' + name
    return subprocess.run(['bash', '-c', program, 'test', str(root)],
                          text=True, capture_output=True, timeout=15)


class StartupGuardTests(unittest.TestCase):
    def test_missing_config_does_not_create_startup_references(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            file = root / 'internal/startup/dependencies.go'
            file.parent.mkdir(parents=True)
            file.write_text('package startup\nimport "github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"\n')
            before = file.read_bytes()
            result = execute(root, 'patch_startup_state_store_cache_and_river_lane')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(file.read_bytes(), before)

    def test_unsupported_database_alias_preserves_composition(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            cfg = root / 'internal/config/config.go'
            cfg.parent.mkdir(parents=True)
            cfg.write_text('\n'.join(f' {name} string' for name in
                           ['StateStore', 'StateStoreCacheDriver', 'StateStoreCacheTTL', 'StateStoreCacheNegativeTTL', 'RedisPrefix']))
            file = root / 'internal/startup/dependencies.go'
            file.parent.mkdir(parents=True)
            file.write_text('''package startup
import (
 "github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
 kitdb "github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
 rediskit "github.com/nmxmxh/ovasabi_foundation/server-kit/go/redis"
)
''')
            before = file.read_bytes()
            result = execute(root, 'patch_startup_state_store_cache_and_river_lane')
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(file.read_bytes(), before)

    def test_readiness_uses_context_logger_and_actual_pool(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            main = root / 'cmd/worker/main.go'
            main.parent.mkdir(parents=True)
            main.write_text('''package main
func main() {
\triverClient, err := river.NewClient(riverpgxv5.New(dbPool), cfg)
\t_ = err
\t// Start River Client
\tif err := riverClient.Start(ctx); err != nil {
\t\tlogger.ErrorContext(ctx, "failed to start River client", "error", err)
\t}
}
''')
            result = execute(root, 'patch_river_table_readiness_gate')
            self.assertEqual(result.returncode, 0, result.stderr)
            text = main.read_text()
            self.assertIn('WaitForRiverTableReady(ctx, dbPool,', text)
            self.assertIn('logger.ErrorContext(ctx, "river_job table not ready"', text)
            first = main.read_bytes()
            self.assertEqual(execute(root, 'patch_river_table_readiness_gate').returncode, 0)
            self.assertEqual(main.read_bytes(), first)


if __name__ == '__main__':
    unittest.main()

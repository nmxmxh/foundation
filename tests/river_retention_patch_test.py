"""Verify bounded retention adoption and preservation of custom periods."""

import importlib.util
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("retention_patch", ROOT / "tooling/scripts/river_retention_patch.py")
migration = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(migration)

CONFIG = '''package config
import ("os"; "time")
type Config struct { Custom string }
func Load() *Config { return &Config{Custom: "preserved"} }
func getEnv(key, fallback string) string { return fallback }
'''
CLIENT = '''package worker
import ("github.com/riverqueue/river")
func main() {
    riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
        Workers: customWorkers,
        Queues: customQueues,
    })
    _ = riverClient
    _ = err
}
'''.replace('    ', '\t')


class RetentionMigrationTests(unittest.TestCase):
    def project(self, directory, client=CLIENT):
        root = Path(directory)
        cfg = root / "internal/config/config.go"
        main = root / "cmd/worker/main.go"
        cfg.parent.mkdir(parents=True)
        main.parent.mkdir(parents=True)
        cfg.write_text(CONFIG)
        main.write_text(client)
        return root, cfg, main

    def test_preserves_custom_workers_and_repeats_without_changes(self):
        with tempfile.TemporaryDirectory() as directory:
            root, cfg, main = self.project(directory)
            self.assertEqual(len(migration.apply(root)), 2)
            self.assertIn('Custom: "preserved"', cfg.read_text())
            self.assertIn('Workers: customWorkers', main.read_text())
            self.assertIn('Queues:  customQueues', main.read_text())
            self.assertEqual(migration.apply(root), [])

    def test_preserves_explicit_retention(self):
        source = '''package startup
import ("github.com/riverqueue/river")
func start() {
\triverCfg := &river.Config{
\t\tCompletedJobRetentionPeriod: 3 * time.Hour,
\t\tCancelledJobRetentionPeriod: 6 * time.Hour,
\t\tDiscardedJobRetentionPeriod: 12 * time.Hour,
\t}
\tclient, err := river.NewClient(riverpgxv5.New(pool), riverCfg)
}
'''
        result = migration.patch_client(source)
        for period in ['3 * time.Hour', '6 * time.Hour', '12 * time.Hour']:
            self.assertIn(period, result)
        self.assertIn('cfg.RiverRetentionTier', result)

    def test_migrates_legacy_startup_signature(self):
        source = '''package startup
import ("github.com/riverqueue/river")
func initWorkerEngine(ctx context.Context, projected *hermes.ProjectedRuntimeStore) {
\triverCfg := &river.Config{}
\tclient, err := river.NewClient(riverpgxv5.New(pool), riverCfg)
}
func init() { initWorkerEngine(ctx, deps.Projected, log) }
'''
        result = migration.patch_client(source)
        self.assertIn('ctx context.Context, cfg *config.Config', result)
        self.assertIn('initWorkerEngine(ctx, cfg, deps.Projected,', result)

    def test_refuses_unknown_client_without_partial_writes(self):
        with tempfile.TemporaryDirectory() as directory:
            root, cfg, main = self.project(directory, 'package worker\nfunc start() { river.NewClient(driver, customConfig) }\n')
            before = cfg.read_bytes()
            with self.assertRaises(ValueError):
                migration.apply(root)
            self.assertEqual(cfg.read_bytes(), before)

    def test_configuration_compiles_when_duration_helper_is_missing(self):
        with tempfile.TemporaryDirectory() as directory:
            root, cfg, _ = self.project(directory)
            migration.apply(root)
            result = subprocess.run(['go', 'test', cfg.name], cwd=cfg.parent,
                                    text=True, capture_output=True, timeout=30)
            self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == '__main__':
    unittest.main()

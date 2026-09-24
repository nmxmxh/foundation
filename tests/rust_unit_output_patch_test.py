#!/usr/bin/env python3
"""Regression coverage for the required Rust unit migration."""
import importlib.util
from pathlib import Path
import tempfile
import unittest
import subprocess
import sys

sys.dont_write_bytecode = True

ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    'migration', ROOT / 'tooling/scripts/rust_unit_output_patch.py')
migration = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(migration)


def unit(body, signature='fn run(&self, input: &[u8]) -> Result<Vec<u8>, String>'):
    return 'impl RuntimeUnit for Example { ' + signature + ' { ' + body + ' } }'


class MigrationTests(unittest.TestCase):
    def test_echo_uses_destination_and_is_idempotent(self):
        after, count = migration.migrate(unit('Ok(input.to_vec())'))
        self.assertEqual(count, 1)
        self.assertIn('__ovrt_output.write(input)', after)
        self.assertEqual(migration.migrate(after), (after, 0))

    def test_unknown_field_ownership_is_preserved(self):
        after, _ = migration.migrate(unit('Ok(self.bytes.to_vec())'))
        self.assertIn('Ok(self.bytes.to_vec())', after)
        self.assertIn('__ovrt_output.write_owned(__ovrt_run()?)', after)

    def test_returns_literals_comments_and_lifetimes_survive(self):
        body = '''
            /* nested { /* fn run(&self) {} */ } */
            let text: &'static str = r###" } fn run( { "###;
            let close = '}'; // fn run(&self) {}
            if input.is_empty() { return Err(text.into()); }
            let output = encode(input)?;
            Ok(output)
        '''
        after, count = migration.migrate(unit(body))
        self.assertEqual(count, 1)
        self.assertIn(body, after)
        self.assertIn('let __ovrt_run = || -> Result<Vec<u8>, String>', after)
        self.assertIn('__ovrt_output.write_owned(__ovrt_run()?)', after)

    def test_custom_signatures_and_conflicts_fail(self):
        for source in (
            unit('Ok(vec![])', 'fn run(&self, input: Bytes) -> Output'),
            unit('Ok(vec![])')[:-1] + ' fn execute(&self) {} }',
            unit('let __ovrt_output = 1; Ok(vec![])'),
        ):
            with self.subTest(source=source), self.assertRaises(ValueError):
                migration.migrate(source)

    def test_plan_preserves_all_sources_on_validation_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'rust').mkdir()
            good = root / 'rust/a.rs'
            good.write_text(unit('Ok(input.to_vec())'))
            (root / 'rust/b.rs').write_text(unit('Ok(vec![])', 'fn run(&self) -> Output'))
            before = good.read_bytes()
            with self.assertRaises(ValueError):
                migration.plan(root, ['rust'])
            self.assertEqual(good.read_bytes(), before)

    def test_targets_are_excluded_and_symlinks_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'rust/target').mkdir(parents=True)
            (root / 'rust/target/generated.rs').write_text('"unterminated')
            self.assertEqual(migration.plan(root, ['rust']), [])
            (root / 'rust/source.rs').symlink_to(root / 'rust/target/generated.rs')
            with self.assertRaises(ValueError):
                migration.plan(root, ['rust'])

    def test_lock_detection_targets_only_unmigrated_unit_dependencies(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'rust').mkdir()
            (root / 'rust/Cargo.toml').write_text('[workspace]\n')
            lock = root / 'rust/Cargo.lock'
            lock.write_text('[[package]]\nname = "ovrt-unit"\ndependencies = ["ovrt-core"]\n')
            command = [sys.executable, str(SPEC.origin), str(root), '--stale-locks']
            result = subprocess.run(command, check=True, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.stdout.strip(), str(root / 'rust/Cargo.toml'))
            lock.write_text('[[package]]\nname = "ovrt-unit"\ndependencies = ["arc-swap", "ovrt-core"]\n')
            result = subprocess.run(command, check=True, capture_output=True, text=True, timeout=10)
            self.assertEqual(result.stdout, '')


if __name__ == '__main__':
    unittest.main()

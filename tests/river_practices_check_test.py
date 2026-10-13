"""Verify detection of queue indexes across supported SQL layouts."""

from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class RiverIndexGateTests(unittest.TestCase):
    def check(self, sql):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / '.foundation').write_text('PROFILE=backend\n')
            (root / 'schema.sql').write_text(sql)
            result = subprocess.run(['zsh', str(ROOT / 'tooling/scripts/river_practices_check.sh'), directory],
                                    text=True, capture_output=True, timeout=15)
            return result.stdout

    def test_rejects_multiline_schema_quoted_and_concurrent_indexes(self):
        for sql in ['CREATE INDEX x ON river_job USING GIN (args);',
                    'CREATE INDEX CONCURRENTLY IF NOT EXISTS x\nON "public"."river_job"\nUSING\ngin (metadata);',
                    'create index x on only public.river_job using gin (args);']:
            with self.subTest(sql=sql):
                self.assertIn('[FAIL] queue GIN policy', self.check(sql))

    def test_accepts_unrelated_gin_and_queue_btree(self):
        for sql in ['CREATE INDEX x ON app_documents USING GIN (data);',
                    'CREATE INDEX x ON public.river_job USING btree (kind);']:
            with self.subTest(sql=sql):
                self.assertIn('[OK] queue GIN policy', self.check(sql))

    def test_preserves_exact_legacy_initial_migration(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / 'migrations').mkdir()
            (root / 'migrations/000001_init.up.sql').write_text(
                'CREATE INDEX river_job_args_index ON river_job USING GIN (args);\n'
                'CREATE INDEX river_job_metadata_index ON river_job USING GIN (metadata);\n')
            command = ['python3', str(ROOT / 'tooling/scripts/queue_gin_check.py'), directory]
            result = subprocess.run(command, text=True, capture_output=True, timeout=15)
            self.assertEqual(result.returncode, 0, result.stdout)
            self.assertIn('[WARN] legacy queue GIN index', result.stdout)
            (root / 'migrations/000040_restore.down.sql').write_text(
                'CREATE INDEX IF NOT EXISTS river_job_args_index ON river_job USING GIN (args);')
            self.assertEqual(subprocess.run(command, capture_output=True, timeout=15).returncode, 0)
            (root / 'migrations/000002_new.up.sql').write_text(
                'CREATE INDEX river_job_args_index ON river_job USING GIN (args);')
            self.assertEqual(subprocess.run(command, capture_output=True, timeout=15).returncode, 1)

    def test_inspection_limit_fails_explicitly(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / 'large.sql'
            with path.open('wb') as file:
                file.truncate(65 * 1024 * 1024)
            result = subprocess.run(['python3', str(ROOT / 'tooling/scripts/queue_gin_check.py'), directory],
                                    text=True, capture_output=True, timeout=15)
            self.assertEqual(result.returncode, 1)
            self.assertIn('SQL inventory exceeds', result.stderr)


if __name__ == '__main__':
    unittest.main()

"""Verify security patch adoption, preservation, and version drift rejection."""

from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
PATCHER = ROOT / 'tooling/scripts/scaffold_managed_patches.sh'
CHECKER = ROOT / 'tooling/scripts/version_consistency_check.sh'
FLOOR = next(row.split('\t')[1] for row in
             (ROOT / 'tooling/versions.tsv').read_text().splitlines()
             if row.startswith('GO_VERSION\t'))


class GoSecurityUpdateTests(unittest.TestCase):
    def patch(self, root):
        return subprocess.run(['bash', str(PATCHER), str(root), '--only', 'go_security'],
                              check=True, capture_output=True, text=True, timeout=20)

    def test_old_requirements_and_effective_build_defaults_converge(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            sources = {
                'go.mod': 'module test.local/app\n\ngo 1.26.0\ntoolchain go1.26.9\n',
                'go.work': 'go 1.26.0\nuse .\n',
                '.env': 'GO_VERSION=1.26.6\nAPP_SETTING=preserved\n',
                'Dockerfile': 'ARG GO_VERSION=1.26\nFROM golang:${GO_VERSION}-alpine\n',
                'docker-compose.yml': 'GO_VERSION: "${GO_VERSION:-1.26}"\n',
            }
            for name, text in sources.items():
                (root / name).write_text(text)
            self.patch(root)
            self.assertIn(f'go {FLOOR}\n', (root / 'go.mod').read_text())
            self.assertIn(f'toolchain go{FLOOR}\n', (root / 'go.mod').read_text())
            self.assertIn(f'go {FLOOR}\n', (root / 'go.work').read_text())
            self.assertEqual((root / '.env').read_text(),
                             f'GO_VERSION={FLOOR}\nAPP_SETTING=preserved\n')
            for name in ['Dockerfile', 'docker-compose.yml']:
                self.assertIn(FLOOR, (root / name).read_text())
            before = {p.name: p.read_bytes() for p in root.iterdir()}
            self.patch(root)
            self.assertEqual(before, {p.name: p.read_bytes() for p in root.iterdir()})

    def test_newer_requirements_and_custom_configuration_survive(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            source = 'module test.local/app\n\ngo 1.27.3\ntoolchain go1.27.3\nreplace test.local/lib => ../lib\n'
            (root / 'go.mod').write_text(source)
            self.patch(root)
            self.assertEqual(source, (root / 'go.mod').read_text())

    def test_version_guard_rejects_stale_patches(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            paths = re.findall(r'^"[A-Z_]+\t([^\t]+)\t', CHECKER.read_text(), re.M)
            paths += ['tooling/versions.tsv']
            for name in set(paths):
                dst = root / name
                dst.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / name, dst)
            command = ['zsh', str(CHECKER), str(root)]
            subprocess.run(command, check=True, capture_output=True, timeout=10)
            for name, old, new in [
                ('server-kit/go/go.mod', f'go {FLOOR}', 'go 1.26.9'),
                ('runtime-sdk/go/go.mod', f'go {FLOOR}', 'go 1.26.9'),
                ('templates/docker/Dockerfile', f'GO_VERSION={FLOOR}', 'GO_VERSION=1.26'),
            ]:
                with self.subTest(name=name):
                    path = root / name
                    source = path.read_text()
                    self.assertIn(old, source)
                    path.write_text(source.replace(old, new))
                    result = subprocess.run(command, capture_output=True, timeout=10)
                    self.assertNotEqual(result.returncode, 0)
                    path.write_text(source)


if __name__ == '__main__':
    unittest.main()

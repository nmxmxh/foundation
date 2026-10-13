"""Verify diagnostic parsing, deadlines, and remote argument boundaries."""

import argparse
import importlib.util
import pathlib
import shlex
import subprocess
import unittest
from unittest.mock import patch

ROOT = pathlib.Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location(
    "profiler", ROOT / "tooling/scripts/deep_resource_profiler.py"
)
profiler = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(profiler)


def options(**values):
    defaults = dict(ssh_target="", ssh_port=22, container="", user="postgres",
                    database="postgres", dsn="", host="localhost", port=5432, limit=10)
    defaults.update(values)
    return argparse.Namespace(**defaults)


class ProfilerTests(unittest.TestCase):
    def execute(self, output, opts=None, code=0):
        with patch.object(profiler.shutil, "which", return_value="/bin/psql"), \
                patch.object(profiler.subprocess, "run", return_value=subprocess.CompletedProcess(
                    [], code, output, "private connection details")) as run:
            rows = profiler.run_query("SELECT 1;", opts or options())
            return rows, run.call_args

    def test_csv_preserves_commas_tabs_and_newlines(self):
        rows, _ = self.execute('table_name,count\n"table,with\ttab\nand newline",12\n')
        self.assertEqual(rows, [{"table_name": "table,with\ttab\nand newline", "count": "12"}])

    def test_empty_result_and_unknown_measurement(self):
        self.assertEqual(self.execute("table_name,count\n")[0], [])
        self.assertEqual(self.execute("heap_hit_ratio,idx_hit_ratio\n,\n")[0],
                         [{"heap_hit_ratio": "", "idx_hit_ratio": ""}])

    def test_malformed_rows_fail(self):
        for output in ["a,b\n1\n", "a,b\n1,2,3\n"]:
            with self.subTest(output=output), self.assertRaises(RuntimeError):
                self.execute(output)

    def test_transaction_and_process_bounds(self):
        _, call = self.execute("value\n1\n")
        cmd = call.args[0]
        for arg in ["-X", "-q", "-w", "--csv", "ON_ERROR_STOP=1"]:
            self.assertIn(arg, cmd)
        self.assertNotIn("-c", cmd)
        sql = call.kwargs["input"]
        self.assertTrue(sql.startswith("BEGIN READ ONLY;"))
        self.assertIn("SET LOCAL statement_timeout = '5s';", sql)
        self.assertIn("SET LOCAL lock_timeout = '1s';", sql)
        self.assertTrue(sql.endswith("COMMIT;\n"))
        self.assertEqual(call.kwargs["timeout"], 15)
        self.assertEqual(call.kwargs["env"]["PGCONNECT_TIMEOUT"], "5")

    def test_remote_shell_preserves_literal_arguments(self):
        for container in ["", "db; echo injected"]:
            opts = options(ssh_target="host", container=container,
                           user="user$(echo injected)", database="db' with spaces")
            _, call = self.execute("value\n1\n", opts)
            cmd = call.args[0]
            self.assertIn("BatchMode=yes", cmd)
            self.assertEqual(cmd[-3:-1], ["--", "host"])
            remote = shlex.split(cmd[-1])
            self.assertIn(opts.user, remote)
            self.assertIn(opts.database, remote)
            if container:
                self.assertIn(container, remote)
            self.assertNotIn("SELECT", cmd[-1])

    def test_container_receives_sql_through_stdin(self):
        _, call = self.execute("value\n1\n", options(container="db"))
        self.assertEqual(call.args[0][:6],
                         ["docker", "exec", "-i", "-e", "PGCONNECT_TIMEOUT=5", "db"])
        self.assertIn("SELECT 1;", call.kwargs["input"])

    def test_failure_does_not_expose_client_output(self):
        with self.assertRaisesRegex(RuntimeError, "exit 3") as failure:
            self.execute("private", code=3)
        self.assertNotIn("private", str(failure.exception))

    def test_timeout_does_not_expose_command(self):
        with patch.object(profiler.shutil, "which", return_value="/bin/psql"), \
                patch.object(profiler.subprocess, "run", side_effect=subprocess.TimeoutExpired(
                    ["private"], 15)), self.assertRaisesRegex(RuntimeError, "15-second") as failure:
            profiler.run_query("SELECT 1;", options())
        self.assertNotIn("private", str(failure.exception))

    def test_limit_rejected_before_connection(self):
        for value in ["0", "-1", "1001"]:
            with patch.object(profiler.sys, "argv", ["profiler", "--limit", value]), \
                    patch.object(profiler.subprocess, "run") as run, \
                    patch.object(profiler.sys, "stderr"), self.assertRaises(SystemExit):
                profiler.main()
            run.assert_not_called()

    def test_entrypoint_matches_tooling_source(self):
        self.assertEqual((ROOT / "scripts/deep_resource_profiler.py").read_bytes(),
                         (ROOT / "tooling/scripts/deep_resource_profiler.py").read_bytes())


if __name__ == "__main__":
    unittest.main()

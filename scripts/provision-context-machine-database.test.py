"""Run against the disposable local PostgreSQL used by CI, never a hosted database."""
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch
from urllib.parse import urlsplit, urlunsplit

SCRIPT = Path(__file__).with_name("provision-context-machine-database.py")


@unittest.skipUnless(os.environ.get("TEST_DATABASE_URL"), "local TEST_DATABASE_URL required")
class ProvisionTest(unittest.TestCase):
    def test_reader_grants_login_and_repeat_refusal(self):
        import importlib.util
        spec = importlib.util.spec_from_file_location("provision", SCRIPT)
        provision = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(provision)
        uri = os.environ["TEST_DATABASE_URL"]
        self.assertIn(urlsplit(uri).hostname, ("localhost", "127.0.0.1", "::1"))
        _, env = provision.connection_env(uri)
        def sql(statement, connection=env):
            return subprocess.run(["psql", "-X", "-At", "-v", "ON_ERROR_STOP=1"], input=statement, text=True, env=connection, capture_output=True)
        self.assertEqual(sql("SELECT count(*) FROM pg_roles WHERE rolname='context_machine_reader'").stdout.strip(), "0", "Use a disposable cluster without an existing reader")
        self.addCleanup(lambda: sql("DROP OWNED BY context_machine_reader; DROP ROLE context_machine_reader"))
        with tempfile.TemporaryDirectory() as directory:
            password = "synthetic-local-only-" + "x" * 48
            run_env = dict(os.environ, MIGRATION_DATABASE_URL=uri, DATABASE_URL=uri,
                           CONTEXT_MACHINE_DATABASE_PASSWORD=password, CONTEXT_MACHINE_PROVISION_OUTPUT=directory)
            result = subprocess.run(["python3", str(SCRIPT)], env=run_env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stdout)
            self.assertEqual(sql("SELECT rolpassword LIKE 'SCRAM-SHA-256$%' FROM pg_authid WHERE rolname='context_machine_reader'").stdout.strip(), "t")
            self.assertNotIn(password, provision.scram_verifier(password))
            metadata = Path(directory, "connection.json").read_text()
            self.assertNotIn(password, metadata + result.stdout + result.stderr)
            u = urlsplit(json.loads(metadata)["connection_without_password"])
            reader_uri = urlunsplit((u.scheme, u.netloc.replace("@", ":"+password+"@", 1), u.path, u.query, ""))
            _, reader = provision.connection_env(reader_uri)
            self.assertEqual(sql("SHOW default_transaction_read_only", reader).stdout.strip(), "on")
            self.assertEqual(sql("SELECT has_table_privilege(current_user,'evidence','SELECT'),has_table_privilege(current_user,'evidence','UPDATE'),has_table_privilege(current_user,'quickbooks_connections','SELECT')", reader).stdout.strip(), "t|f|f")
            repeat = subprocess.run(["python3", str(SCRIPT)], env=run_env, capture_output=True, text=True)
            self.assertNotEqual(repeat.returncode, 0)
            self.assertNotIn(password, repeat.stdout + repeat.stderr)
            self.assertEqual(repeat.stdout.strip(), "The dedicated role already exists; no credential rotation was attempted")
            self.assertNotIn("SCRAM-SHA-256$", repeat.stdout + repeat.stderr)
            self.assertEqual(sql("SELECT 1", reader).returncode, 0, "A repeated run must not rotate the existing credential")
            self.assertEqual(sql("DROP OWNED BY context_machine_reader; DROP ROLE context_machine_reader").returncode, 0)
            self.assertEqual(sql("GRANT CREATE ON SCHEMA public TO PUBLIC").returncode, 0)
            try:
                unsafe = subprocess.run(["python3", str(SCRIPT)], env=run_env, capture_output=True, text=True)
                self.assertNotEqual(unsafe.returncode, 0)
                self.assertNotIn(password, unsafe.stdout + unsafe.stderr)
                self.assertEqual(unsafe.stdout.strip(), "Unsafe existing PUBLIC privileges; role creation rolled back")
                self.assertNotIn("SCRAM-SHA-256$", unsafe.stdout + unsafe.stderr)
                self.assertEqual(sql("SELECT count(*) FROM pg_roles WHERE rolname='context_machine_reader'").stdout.strip(), "0", "Unsafe PUBLIC permissions must roll back role creation")
                self.assertFalse(Path(directory, "connection.json").exists())
            finally:
                sql("REVOKE CREATE ON SCHEMA public FROM PUBLIC")
            failure = subprocess.CompletedProcess([], 3, stdout=" connected\n created-reader\n", stderr="ERROR:  42P01: " + password + " SCRAM-SHA-256$private")
            with patch.dict(os.environ, run_env), patch.object(provision.subprocess, "run", return_value=failure):
                with self.assertRaises(provision.ProvisionError) as caught:
                    provision.main()
            self.assertIn("SQLSTATE 42P01", str(caught.exception))
            self.assertIn("last phase: created-reader", str(caught.exception))
            self.assertNotIn(password, str(caught.exception))
            self.assertNotIn("SCRAM-SHA-256$", str(caught.exception))


if __name__ == "__main__":
    unittest.main()

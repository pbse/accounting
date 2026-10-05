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
            run_env = dict(os.environ, MIGRATION_DATABASE_URL=uri, DATABASE_RUNTIME_ROLE="close_runtime", DATABASE_URL="",
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
            # Exercise Neon's plaintext-only path on local PostgreSQL, with real login.
            create = provision.create_role_sql(password, "ep-test.neon.tech")
            self.assertNotIn("PASSWORD '" + password, create)
            original_create = provision.create_role_sql
            with patch.dict(os.environ, run_env), patch.object(provision, "create_role_sql", side_effect=lambda value, host: original_create(value, "ep-test.neon.tech")):
                provision.main()
                self.assertEqual(sql("SELECT rolpassword LIKE 'SCRAM-SHA-256$%' FROM pg_authid WHERE rolname='context_machine_reader'").stdout.strip(), "t")
                self.assertEqual(sql("SHOW default_transaction_read_only", reader).stdout.strip(), "on")
                self.assertEqual(sql("SELECT has_table_privilege(current_user,'evidence','SELECT'),has_table_privilege(current_user,'evidence','UPDATE')", reader).stdout.strip(), "t|f")
                with self.assertRaises(provision.ProvisionError) as repeated:
                    provision.main()
                self.assertIn("already exists", str(repeated.exception))
                self.assertEqual(sql("SELECT 1", reader).returncode, 0)
                self.assertEqual(sql("DROP OWNED BY context_machine_reader; DROP ROLE context_machine_reader").returncode, 0)
                self.assertEqual(sql("GRANT CREATE ON SCHEMA public TO PUBLIC").returncode, 0)
                try:
                    with self.assertRaises(provision.ProvisionError) as unsafe_neon:
                        provision.main()
                    self.assertIn("Unsafe existing PUBLIC privileges", str(unsafe_neon.exception))
                    self.assertEqual(sql("SELECT count(*) FROM pg_roles WHERE rolname='context_machine_reader'").stdout.strip(), "0")
                    self.assertFalse(Path(directory, "connection.json").exists())
                finally:
                    sql("REVOKE CREATE ON SCHEMA public FROM PUBLIC")
            # Permission failure must not echo a dynamic password-bearing CREATE ROLE.
            self.assertEqual(sql("CREATE ROLE provision_denied_owner").returncode, 0)
            try:
                denied = sql("BEGIN; SET LOCAL ROLE provision_denied_owner; " + create + "COMMIT;")
                self.assertNotEqual(denied.returncode, 0)
                self.assertNotIn(password, denied.stdout + denied.stderr)
                self.assertIn("permission denied creating reader", denied.stderr)
            finally:
                sql("DROP ROLE provision_denied_owner")
            failure = subprocess.CompletedProcess([], 3, stdout=" connected\n created-reader\n", stderr="ERROR:  42P01: " + password + " SCRAM-SHA-256$private")
            with patch.dict(os.environ, run_env), patch.object(provision.subprocess, "run", return_value=failure):
                with self.assertRaises(provision.ProvisionError) as caught:
                    provision.main()
            self.assertIn("SQLSTATE 42P01", str(caught.exception))
            self.assertIn("last phase: created-reader", str(caught.exception))
            self.assertNotIn(password, str(caught.exception))
            self.assertNotIn("SCRAM-SHA-256$", str(caught.exception))


    def test_runtime_role_checked_before_schema_changes(self):
        import secrets
        uri = os.environ["TEST_DATABASE_URL"]
        u = urlsplit(uri)
        self.assertIn(u.hostname, ("localhost", "127.0.0.1", "::1"))
        suffix = secrets.token_hex(4)
        database = "migration_guard_" + suffix
        runtime = "runtime_guard_" + suffix
        group = "group_guard_" + suffix
        def sql(statement, connection=uri):
            return subprocess.run(["psql", connection, "-X", "-At", "-v", "ON_ERROR_STOP=1"],
                                  input=statement, env=os.environ, capture_output=True, text=True)
        self.assertEqual(sql(f'CREATE DATABASE {database}').returncode, 0)
        fresh = urlunsplit((u.scheme, u.netloc, "/" + database, u.query, ""))
        runner = str(SCRIPT.parents[1] / "infra/postgres/migrate.sh")
        try:
            self.assertEqual(sql(f'CREATE ROLE {runtime} LOGIN; CREATE ROLE {group} NOLOGIN').returncode, 0)
            def run(role):
                return subprocess.run([runner], env=dict(os.environ, DATABASE_URL=fresh, MIGRATION_GRANT_TO=role), capture_output=True, text=True)
            for role in ["missing_role_" + suffix, u.username, group, "context_machine_reader", 'bad"; SELECT 1; --']:
                result = run(role)
                self.assertNotEqual(result.returncode, 0, role)
                self.assertEqual(sql("SELECT to_regclass('migrations.applied') IS NULL", fresh).stdout.strip(), "t")
            self.assertEqual(sql(f'ALTER ROLE {runtime} CREATEDB').returncode, 0)
            self.assertNotEqual(run(runtime).returncode, 0)
            self.assertEqual(sql(f'ALTER ROLE {runtime} NOCREATEDB; GRANT "{u.username}" TO {runtime}').returncode, 0)
            self.assertNotEqual(run(runtime).returncode, 0)
            self.assertEqual(sql(f'REVOKE "{u.username}" FROM {runtime}; ALTER ROLE {runtime} SET default_transaction_read_only=on').returncode, 0)
            self.assertNotEqual(run(runtime).returncode, 0)
            self.assertEqual(sql(f'ALTER ROLE {runtime} RESET default_transaction_read_only').returncode, 0)
            result = run(runtime)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(sql(f"SELECT has_table_privilege('{runtime}','evidence','INSERT')", fresh).stdout.strip(), "t")
            repeated = run(runtime)
            self.assertEqual(repeated.returncode, 0, repeated.stderr)
            self.assertEqual(repeated.stdout, "")
        finally:
            sql(f'DROP DATABASE {database} WITH (FORCE)')
            sql(f'DROP ROLE IF EXISTS {runtime}; DROP ROLE IF EXISTS {group}')


if __name__ == "__main__":
    unittest.main()

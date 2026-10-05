"""Provision the dedicated reader using the existing owner credential; never log passwords."""
import base64
import hashlib
import hmac
import json
import secrets
import os
from pathlib import Path
import re
import subprocess
from urllib.parse import parse_qs, unquote, urlencode, urlsplit, urlunsplit

ROLE = "context_machine_reader"


class ProvisionError(Exception):
    """Only fixed, credential-free operator diagnostics belong in this exception."""


def connection_env(uri):
    u = urlsplit(uri)
    query = parse_qs(u.query)
    sslmode = query.get("sslmode", [""])[0]
    if u.scheme not in ("postgres", "postgresql") or not u.hostname or not u.username or not u.path.strip("/"):
        raise ProvisionError("A PostgreSQL connection is required")
    if u.hostname not in ("localhost", "127.0.0.1", "::1") and (sslmode not in ("require", "verify-ca", "verify-full") or "-pooler" in u.hostname):
        raise ProvisionError("Hosted provisioning requires a direct PostgreSQL endpoint with TLS")
    env = dict(os.environ, PGHOST=u.hostname, PGPORT=str(u.port or 5432), PGUSER=unquote(u.username),
               PGPASSWORD=unquote(u.password or ""), PGDATABASE=unquote(u.path[1:]),
               PGSSLMODE=sslmode or "disable", PGCHANNELBINDING="disable" if u.hostname in ("localhost", "127.0.0.1", "::1") else "require",
               PGCONNECT_TIMEOUT="15", PGAPPNAME="context-machine-provision")
    return u, env


def scram_verifier(password):
    # PostgreSQL's stored SCRAM format; ASCII URL-safe passwords need no SASLprep.
    # https://github.com/postgres/postgres/blob/master/src/common/scram-common.c
    salt = secrets.token_bytes(16)
    salted = hashlib.pbkdf2_hmac("sha256", password.encode("ascii"), salt, 4096)
    stored = hashlib.sha256(hmac.digest(salted, b"Client Key", "sha256")).digest()
    server = hmac.digest(salted, b"Server Key", "sha256")
    b64 = lambda value: base64.b64encode(value).decode("ascii")
    return f"SCRAM-SHA-256$4096:{b64(salt)}${b64(stored)}:{b64(server)}"


def create_role_sql(password, hostname):
    attributes = "NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOREPLICATION NOBYPASSRLS"
    if not hostname.endswith(".neon.tech"):
        return f"CREATE ROLE {ROLE} LOGIN PASSWORD '{scram_verifier(password)}' {attributes};"
    # Neon requires plaintext: https://neon.com/docs/manage/roles#manage-roles-with-sql
    # COPY sends data separately from SQL statements. The DO body contains no
    # credential; its exception handler prevents dynamic SQL appearing in errors.
    return f"""
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='{ROLE}') THEN
  RAISE EXCEPTION 'Dedicated role already exists';
 END IF;
END $$;
CREATE TEMP TABLE context_machine_password(value text) ON COMMIT DROP;
COPY pg_temp.context_machine_password(value) FROM STDIN;
{password}
\\.
DO $$ BEGIN
 EXECUTE format('CREATE ROLE %I LOGIN PASSWORD %L {attributes}',
                '{ROLE}', (SELECT value FROM pg_temp.context_machine_password));
EXCEPTION WHEN OTHERS THEN
 IF SQLSTATE='42501' THEN
  RAISE EXCEPTION 'permission denied creating reader' USING ERRCODE=SQLSTATE;
 ELSIF SQLSTATE='42710' THEN
  RAISE EXCEPTION 'Dedicated role already exists' USING ERRCODE=SQLSTATE;
 ELSE
  RAISE EXCEPTION 'Reader creation rejected by provider' USING ERRCODE=SQLSTATE;
 END IF;
END $$;
"""


def main():
    owner, env = connection_env(os.environ["MIGRATION_DATABASE_URL"])
    runtime_role = os.environ.get("DATABASE_RUNTIME_ROLE")
    if runtime_role:
        if not re.fullmatch(r"[a-zA-Z_][a-zA-Z0-9_]{0,62}", runtime_role):
            raise ProvisionError("A verified application role is required")
    else:
        runtime, _ = connection_env(os.environ["DATABASE_URL"])
        if (owner.hostname, owner.port or 5432, owner.path) != (runtime.hostname, runtime.port or 5432, runtime.path):
            raise ProvisionError("Owner and application connections must target the same database")
        runtime_role = unquote(runtime.username)
    if runtime_role == ROLE:
        raise ProvisionError("The reader must be separate from the application role")
    password = os.environ["CONTEXT_MACHINE_DATABASE_PASSWORD"]
    if not re.fullmatch(r"[A-Za-z0-9_-]{48,128}", password):
        raise ProvisionError("A newly generated reader password of at least 48 characters is required")
    # Prepare password-free metadata before making any database changes.
    query = {"sslmode": env["PGSSLMODE"], "channel_binding": env["PGCHANNELBINDING"]}
    authority = ROLE + "@" + owner.netloc.rsplit("@", 1)[-1]
    uri = urlunsplit((owner.scheme, authority, owner.path, urlencode(query), ""))
    directory = Path(os.environ["CONTEXT_MACHINE_PROVISION_OUTPUT"])
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    (directory / "connection.json").write_text(json.dumps({"connection_without_password": uri}) + "\n")
    grant_file = Path(__file__).resolve().parents[1] / "infra/postgres/context-machine-role.sql"
    sql = grant_file.read_text()
    if sql.count("BEGIN;") != 1 or sql.count("COMMIT;") != 1:
        raise ProvisionError("Expected the canonical grants to contain one transaction")
    # Never rotate an existing role implicitly. A repeated run fails without changes.
    create = create_role_sql(password, owner.hostname)
    sql = sql.replace("BEGIN;", "SELECT 'connected';\nBEGIN;\nSET LOCAL statement_timeout='15s';\nSET LOCAL password_encryption='scram-sha-256';\n" + create + "\nSELECT 'created-reader';", 1)
    sql = sql.replace("COMMIT;", f"""
SELECT 'applied-grants';
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM pg_roles WHERE rolname='{ROLE}' AND (rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole OR rolreplication))
 OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p','v','m','f') AND (has_table_privilege('{ROLE}',c.oid,'INSERT,UPDATE,DELETE,TRUNCATE,TRIGGER') OR c.relowner=(SELECT oid FROM pg_roles WHERE rolname='{ROLE}')))
 OR has_schema_privilege('{ROLE}','public','CREATE')
 OR EXISTS(SELECT 1 FROM pg_auth_members WHERE member=(SELECT oid FROM pg_roles WHERE rolname='{ROLE}')) THEN
  RAISE EXCEPTION 'Unexpected reader privileges; provisioning rolled back';
 END IF;
END $$;
COMMIT;
""", 1)
    result = subprocess.run(["psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-v", "VERBOSITY=verbose", "-v", f"agent_role={ROLE}"],
                            input=sql, text=True, env=env, capture_output=True)
    if result.returncode:
        (directory / "connection.json").unlink(missing_ok=True)
        # SQL errors can echo statements containing credentials. Never relay them.
        reason = "Database provisioning did not complete; check connectivity, role existence and owner privileges"
        for pattern, message in (
            ("already exists", "The dedicated role already exists; no credential rotation was attempted"),
            ("Unexpected reader privileges", "Unsafe existing PUBLIC privileges; role creation rolled back"),
            ("permission denied", "The owner lacks required role/schema/grant permissions; provisioning rolled back"),
            ("password authentication failed", "The production owner credential could not authenticate"),
            ("channel binding", "The production owner connection did not satisfy required channel binding"),
        ):
            if pattern in result.stderr:
                reason = message
                break
        else:
            # Extract only the fixed-width SQLSTATE and our own known phase markers.
            # Never relay arbitrary database error text, SQL, or connection details.
            state = re.search(r"(?:ERROR|FATAL):\s+([0-9A-Z]{5}):", result.stderr)
            phase = "not connected"
            for marker in ("connected", "created-reader", "applied-grants"):
                if marker in {line.strip() for line in result.stdout.splitlines()}:
                    phase = marker
            reason += f" (psql exit {result.returncode}; last phase: {phase}"
            if state:
                reason += f"; SQLSTATE {state.group(1)}"
            reason += ")"
        raise ProvisionError(reason)
    print("Dedicated database reader provisioned; only password-free connection metadata exported.")


if __name__ == "__main__":
    try:
        main()
    except ProvisionError as error:
        print(str(error))
        raise SystemExit(1) from None
    except (KeyError, ValueError, RuntimeError, OSError):
        # Do not print arbitrary exception data or libpq connection strings.
        print("Reader provisioning did not complete. No credentials were logged.")
        raise SystemExit(1) from None

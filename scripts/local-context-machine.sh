#!/usr/bin/env bash
# Starts a context machine on this computer, for evaluation runs only (docs/slice6-evidence.md), with production's model
# settings: Codex gpt-6.1-sol at medium effort, on your ChatGPT/Codex subscription. You run it yourself, in a terminal of
# its own, and leave it running while scripts/evidence-run.sh works; Ctrl-C stops it. The hosted context machine has no
# outside route by design, so evaluations use this one.
#
#   scripts/local-context-machine.sh postgres://close:...@127.0.0.1:5434
#
# The first time, Codex opens its browser login in a home of its own (~/.krith-eval/codex), separate from your usual Codex
# login. Everything it keeps stays in ~/.krith-eval, readable only by you, and nothing is printed. The service holds the
# login's access token as it starts, so a token with less than two hours left asks you to sign in again first.
set -euo pipefail
root=$(git rev-parse --show-toplevel)
server=${1:?local PostgreSQL server URL without a database, e.g. postgres://close:secret@127.0.0.1:5434}
case "$server" in
  postgres://*@127.0.0.1:*|postgres://*@localhost:*|postgres://*@\[::1\]:*) ;;
  *) echo "Use a local PostgreSQL server (127.0.0.1, localhost or [::1])." >&2; exit 2 ;;
esac
home=${KRITH_EVAL_HOME:-$HOME/.krith-eval}
umask 077
mkdir -p "$home/codex"
token() { python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["tokens"][sys.argv[2]])' "$home/codex/auth.json" "$1"; }
# Seconds the saved access token has left, read from its own expiry claim (the token itself is never printed).
left() {
  python3 -c 'import base64,json,sys,time; p=json.load(open(sys.argv[1]))["tokens"]["access_token"].split(".")[1]; print(int(json.loads(base64.urlsafe_b64decode(p+"="*(-len(p)%4)))["exp"]-time.time()))' "$home/codex/auth.json" 2>/dev/null || echo 0
}
if [ ! -s "$home/codex/auth.json" ] || [ "$(left)" -lt 7200 ]; then
  CODEX_HOME="$home/codex" codex -c 'cli_auth_credentials_store="file"' login
fi
# The token evaluation runs present to this service (scripts/evidence-run.sh reads it from here).
[ -s "$home/context-machine-token" ] || openssl rand -hex 32 > "$home/context-machine-token"
# The service requires a read-only database login, granted as production's is (infra/postgres/context-machine-role.sql) on
# a migrated database of its own; model completions never read it.
if [ ! -s "$home/reader-url" ]; then
  password=$(openssl rand -hex 24)
  PGOPTIONS=--client-min-messages=warning psql "$server/postgres" -Xq -v ON_ERROR_STOP=1 >/dev/null <<SQL
\set password '$password'
SELECT 'CREATE ROLE krith_eval_reader LOGIN' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'krith_eval_reader') \gexec
ALTER ROLE krith_eval_reader PASSWORD :'password';
SELECT 'CREATE DATABASE krith_eval' WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'krith_eval') \gexec
SQL
  DATABASE_URL="$server/krith_eval?sslmode=disable" bash "$root/infra/postgres/migrate.sh" >/dev/null
  PGOPTIONS=--client-min-messages=warning psql "$server/krith_eval" -Xq -v agent_role=krith_eval_reader -f "$root/infra/postgres/context-machine-role.sql" >/dev/null
  printf 'postgres://krith_eval_reader:%s@%s/krith_eval?sslmode=disable\n' "$password" "${server#*@}" > "$home/reader-url"
fi
cd "$root/services/backend"
echo "Local context machine on http://127.0.0.1:8090 (Ctrl-C stops it)."
CONTEXT_MACHINE_TOKEN=$(cat "$home/context-machine-token") \
CONTEXT_MACHINE_DATABASE_URL=$(cat "$home/reader-url") \
CONTEXT_MACHINE_MODEL=codex CODEX_MODEL=gpt-6.1-sol CODEX_REASONING_EFFORT=medium CODEX_AUTH_MODE=subscription \
CODEX_ACCESS_TOKEN=$(token access_token) CODEX_ACCOUNT_ID=$(token account_id) \
  exec go run ./cmd/context-machine -addr 127.0.0.1:8090

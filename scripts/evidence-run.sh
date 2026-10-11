#!/usr/bin/env bash
# Runs the release's evidence checks for the current revision and records them in a dated directory (see
# docs/slice6-evidence.md): provider checks, the acceptance corpus with recorded answers and, when the reading models
# are configured, with the live model, and the chat golden-set replay when the chat model is configured. Each harness
# gets a fresh database on the given local PostgreSQL server; nothing hosted is written to. Evidence is kept outside
# the repository by default (EVIDENCE_DIR), because a real corpus run holds client data. A check that cannot run here is
# recorded with what it needs, never as a pass.
#
#   scripts/evidence-run.sh postgres://close:...@127.0.0.1:5434 [corpus-dir]
#
# The corpus defaults to the synthetic sample; the release corpus lives outside the repository.
set -euo pipefail
root=$(git rev-parse --show-toplevel)
server=${1:?local PostgreSQL server URL without a database, e.g. postgres://close:secret@127.0.0.1:5434}
corpus=${2:-$root/services/backend/acceptance/sample}
case "$server" in
  postgres://*@127.0.0.1:*|postgres://*@localhost:*|postgres://*@\[::1\]:*) ;;
  *) echo "Use a local PostgreSQL server (127.0.0.1, localhost or [::1])." >&2; exit 2 ;;
esac
# Reports from a real corpus hold client names and figures: only this user can read them.
umask 077
revision=$(git -C "$root" rev-parse --short HEAD)
out=${EVIDENCE_DIR:-$HOME/krith-evidence}/$(date -u +%Y-%m-%dT%H%M%SZ)-$revision
mkdir -p "$out"
dirty=$(git -C "$root" status --porcelain | grep -v ' gemini_key.txt$' | wc -l | tr -d ' ')

# Which settings were present (names only, never values), so the record says what the run could reach.
present=()
for name in LLM_PROVIDER OPENAI_API_KEY OPENAI_MODEL CONTEXT_MACHINE_ENDPOINT CONTEXT_MACHINE_PROFILES GEMINI_API_KEY GEMINI_PAID_SERVICE PLATFORM_EMAIL_KEY CLIENT_EMAIL_ADDRESS; do
  [ -n "${!name:-}" ] && present+=("\"$name\"")
done
printf '{"revision":"%s","uncommitted_changes":%s,"started_at":"%s","corpus":"%s","settings_present":[%s]}\n' \
  "$(git -C "$root" rev-parse HEAD)" "$dirty" "$(date -u +%FT%TZ)" "$corpus" "$(IFS=,; echo "${present[*]:-}")" > "$out/manifest.json"

fresh() {
  local db="evidence_$1_$$"
  PGOPTIONS=--client-min-messages=warning psql "$server/postgres" -XAtqc "DROP DATABASE IF EXISTS $db" -c "CREATE DATABASE $db" >/dev/null
  DATABASE_URL="$server/$db?sslmode=disable" bash "$root/infra/postgres/migrate.sh" >/dev/null
  echo "$server/$db?sslmode=disable"
}
status() { echo "$1: $2" | tee -a "$out/summary.txt"; }

cd "$root/services/backend"
# A Context Machine reads only for the firm and member its access profile serves: name them for the live runs.
identity=()
if [ -n "${ACCEPTANCE_ORGANIZATION:-}" ]; then identity=(-organization "$ACCEPTANCE_ORGANIZATION" -subject "${ACCEPTANCE_SUBJECT:-}"); fi
if go run ./cmd/provider-check "${identity[@]+"${identity[@]}"}" -report "$out/providers.json" > "$out/providers.md" 2>&1; then status providers "no configured check failed (see providers.md for dependencies)"; else status providers "a configured check FAILED"; fi

if go run ./cmd/acceptance -corpus "$corpus" -database "$(fresh recorded)" -model recorded -report "$out/acceptance-recorded.json" > "$out/acceptance-recorded.md" 2>&1; then
  status "acceptance (recorded answers)" passed
else
  status "acceptance (recorded answers)" "FAILED or critical failures (see acceptance-recorded.md)"
fi
if go run ./cmd/acceptance -corpus "$corpus" -database "$(fresh live)" -model live "${identity[@]+"${identity[@]}"}" -report "$out/acceptance-live.json" > "$out/acceptance-live.md" 2>&1; then
  status "acceptance (live model)" passed
elif grep -q "models configured\|configure" "$out/acceptance-live.md"; then
  status "acceptance (live model)" "dependency: the preparation and evidence_selection models are not configured"
else
  status "acceptance (live model)" "FAILED or critical failures (see acceptance-live.md)"
fi
replay=$(fresh replay)
if ACCEPTANCE_DATABASE_URL=$replay go run ./cmd/agent-eval -set acceptance/agent -report "$out/agent-eval.json" > "$out/agent-eval.md" 2>&1; then
  status "chat golden set (live model)" passed
elif grep -q "chat model configured" "$out/agent-eval.md"; then
  status "chat golden set (live model)" "dependency: the chat model is not configured"
else
  status "chat golden set (live model)" "FAILED (see agent-eval.md)"
fi
echo "Evidence recorded in $out"

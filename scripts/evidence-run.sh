#!/usr/bin/env bash
# Runs the release's evidence checks for the current revision and records them in a dated directory (see
# docs/slice6-evidence.md): provider checks, the acceptance corpus with recorded answers and, when the reading models
# are configured, with the live model, returned drafts' notes read by the live model, and the chat golden-set replay when
# the chat model is configured. Each harness
# gets a fresh database on the given local PostgreSQL server; nothing hosted is written to. Evidence is kept outside
# the repository by default (EVIDENCE_DIR), because a real corpus run holds client data. A check that cannot run here is
# recorded with what it needs, never as a pass.
#
#   scripts/evidence-run.sh postgres://close:...@127.0.0.1:5434 [corpus-dir]
#
# The corpus defaults to the synthetic sample; the release corpus lives outside the repository. The model is a local
# context machine when scripts/local-context-machine.sh is running, as production's is; the live runs share a daily
# ceiling on model calls (EVAL_DAILY_MODEL_CALLS, default 200).
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
evidence=${EVIDENCE_DIR:-$HOME/krith-evidence}
mkdir -p "$evidence"
# One run at a time, so two runs cannot share one allowance or overwrite each other's count.
if ! mkdir "$evidence/.run-lock" 2>/dev/null; then
  echo "Another evidence run is in progress (remove $evidence/.run-lock if none is)." >&2
  exit 2
fi
trap 'rmdir "$evidence/.run-lock"' EXIT
out=$evidence/$(date -u +%Y-%m-%dT%H%M%SZ)-$revision
mkdir -p "$out"
dirty=$(git -C "$root" status --porcelain | grep -v ' gemini_key.txt$' | wc -l | tr -d ' ')

# A running local context machine (scripts/local-context-machine.sh) is the model, as production's context machine is:
# Codex gpt-6.1-sol at medium effort. Its token is read from its file, never printed.
eval_home=${KRITH_EVAL_HOME:-$HOME/.krith-eval}
if [ -s "$eval_home/context-machine-token" ] && printf 'Authorization: Bearer %s\n' "$(cat "$eval_home/context-machine-token")" |
  curl -sf -m 15 -H @- http://127.0.0.1:8090/readyz >/dev/null 2>&1; then
  export LLM_PROVIDER=context_machine CONTEXT_MACHINE_ENDPOINT=http://127.0.0.1:8090/v1/run CONTEXT_MACHINE_MODEL=codex \
    CODEX_MODEL=gpt-6.1-sol CODEX_REASONING_EFFORT=medium LLM_ALLOW_UNCAPPED_OUTPUT_TOKENS=true
  CONTEXT_MACHINE_TOKEN=$(cat "$eval_home/context-machine-token")
  export CONTEXT_MACHINE_TOKEN
fi

# The daily ceiling on the evaluation's model calls, across every run today: the user's $20 a day, at roughly $0.10 a
# call (an API-price estimate for a reading call; revisit it once runs record real token use). The calls go on the
# user's Codex subscription, which production shares, so the ceiling also keeps production's allowance.
ceiling=${EVAL_DAILY_MODEL_CALLS:-200}
ledger=$evidence/model-calls-$(date -u +%F)
left() { echo $((ceiling - $(cat "$ledger" 2>/dev/null || echo 0))); }
spend() { echo $(($(cat "$ledger" 2>/dev/null || echo 0) + $1)) > "$ledger"; }
calls_in() {
  PGOPTIONS=--client-min-messages=warning psql "$1" -XAtq -c "SET app.organization='*'" \
    -c "SELECT COALESCE(sum(calls),0) FROM model_calls WHERE day=(now() AT TIME ZONE 'UTC')::date"
}

# Which settings were present (names only, never values), so the record says what the run could reach.
present=()
for name in LLM_PROVIDER OPENAI_API_KEY OPENAI_MODEL CONTEXT_MACHINE_ENDPOINT CONTEXT_MACHINE_TOKEN CONTEXT_MACHINE_MODEL CODEX_MODEL CONTEXT_MACHINE_PROFILES PLATFORM_EMAIL_KEY CLIENT_EMAIL_ADDRESS; do
  [ -n "${!name:-}" ] && present+=("\"$name\"")
done
printf '{"revision":"%s","uncommitted_changes":%s,"started_at":"%s","corpus":"%s","settings_present":[%s],"daily_model_call_ceiling":%s,"model_calls_before":%s}\n' \
  "$(git -C "$root" rev-parse HEAD)" "$dirty" "$(date -u +%FT%TZ)" "$corpus" "$(IFS=,; echo "${present[*]:-}")" "$ceiling" "$((ceiling - $(left)))" > "$out/manifest.json"

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
# The model checks make one call per configured route, ten at most.
checks=model,email
if [ "$(left)" -lt 10 ]; then checks=email; status "providers (model)" "not run: ceiling reached for today ($ceiling model calls)"; fi
if go run ./cmd/provider-check -only "$checks" "${identity[@]+"${identity[@]}"}" -report "$out/providers.json" > "$out/providers.md" 2>&1; then status providers "no configured check failed (see providers.md for dependencies)"; else status providers "a configured check FAILED"; fi
spend "$(python3 -c 'import json,sys; print(sum(c["provider"] == "model" and c["name"] != "configuration" and c["state"] != "dependency" for c in json.load(open(sys.argv[1]))["checks"]))' "$out/providers.json" 2>/dev/null || echo 0)"

if go run ./cmd/acceptance -corpus "$corpus" -database "$(fresh recorded)" -model recorded -report "$out/acceptance-recorded.json" -review "$out/review-package.md" > "$out/acceptance-recorded.md" 2>&1; then
  status "acceptance (recorded answers)" passed
else
  status "acceptance (recorded answers)" "FAILED or critical failures (see acceptance-recorded.md)"
fi
# The live run's review package, when it runs, replaces the recorded one: the accountant judges the model's work.
live=$(fresh live)
if [ "$(left)" -lt 1 ]; then
  status "acceptance (live model)" "not run: ceiling reached for today ($ceiling model calls)"
elif go run ./cmd/acceptance -corpus "$corpus" -database "$live" -model live -max-model-calls "$(left)" "${identity[@]+"${identity[@]}"}" -report "$out/acceptance-live.json" -review "$out/review-package.md" > "$out/acceptance-live.md" 2>&1; then
  status "acceptance (live model)" passed
elif grep -q "Ceiling reached for today" "$out/acceptance-live.md"; then
  status "acceptance (live model)" "ceiling reached for today: cases left unrun (see acceptance-live.md)"
elif grep -q "models configured\|configure" "$out/acceptance-live.md"; then
  status "acceptance (live model)" "dependency: the preparation and evidence_selection models are not configured"
else
  status "acceptance (live model)" "FAILED or critical failures (see acceptance-live.md)"
fi
spend "$(calls_in "$live")"
# Returned drafts' notes read as a close's goal reads them (cmd/revision-eval): a change only where a document or the
# note shows it, one question otherwise. It needs no database; it counts its own calls.
if [ "$(left)" -lt 1 ]; then
  status "returned-draft revisions (live model)" "not run: ceiling reached for today ($ceiling model calls)"
else
  # go run reports any failing exit as 1, so the ceiling is read from what the command printed.
  if go run ./cmd/revision-eval -max-model-calls "$(left)" "${identity[@]+"${identity[@]}"}" -report "$out/revisions.json" > "$out/revisions.md" 2>&1; then
    status "returned-draft revisions (live model)" "no false acceptance or error (see revisions.md)"
  elif grep -q "needs the preparation model" "$out/revisions.md"; then
    status "returned-draft revisions (live model)" "dependency: the preparation model is not configured"
  elif grep -q "Ceiling reached for today" "$out/revisions.md" && ! grep -qE "[1-9][0-9]* false acceptances|[1-9][0-9]* errors" "$out/revisions.md"; then
    status "returned-draft revisions (live model)" "ceiling reached for today: notes left unrun (see revisions.md)"
  else
    status "returned-draft revisions (live model)" "FAILED: a false acceptance or a model error (see revisions.md)"
  fi
  spend "$(sed -n 's/.*Model calls made: \([0-9]*\).*/\1/p' "$out/revisions.md" | head -1 | grep . || echo 0)"
fi
replay=$(fresh replay)
if [ "$(left)" -lt 1 ]; then
  status "chat golden set (live model)" "not run: ceiling reached for today ($ceiling model calls)"
elif ACCEPTANCE_DATABASE_URL=$replay go run ./cmd/agent-eval -set acceptance/agent -max-model-calls "$(left)" -report "$out/agent-eval.json" > "$out/agent-eval.md" 2>&1; then
  status "chat golden set (live model)" passed
elif grep -q "Ceiling reached for today" "$out/agent-eval.md"; then
  status "chat golden set (live model)" "ceiling reached for today: questions left unrun (see agent-eval.md)"
elif grep -q "chat model configured" "$out/agent-eval.md"; then
  status "chat golden set (live model)" "dependency: the chat model is not configured"
else
  status "chat golden set (live model)" "FAILED (see agent-eval.md)"
fi
spend "$(calls_in "$replay")"
status "model calls today" "$((ceiling - $(left))) of $ceiling"
echo "Evidence recorded in $out"

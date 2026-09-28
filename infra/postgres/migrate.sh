#!/usr/bin/env bash
# Applies the numbered migrations a database does not have yet, in order. Each file runs in one transaction together
# with its row in the ledger (migrations.applied), so a file is either applied and recorded or neither. The ledger sits
# outside the public schema, which hosted readiness requires to be entirely under row-level security. Runs are
# serialized by an advisory lock, and a file changed after it was applied stops the run.
#
# DATABASE_URL        the database owner, who may change the schema (required)
# MIGRATION_ADOPT     once, for a database migrated before the ledger: a file of "migration|SQL condition" lines naming
#                     what it already has; each whose condition holds is recorded without being run
# MIGRATION_GRANT_TO  the app's own role, given the app's privileges on every table and sequence afterwards
set -euo pipefail
: "${DATABASE_URL:?Set DATABASE_URL to the database owner}"
dir=$(CDPATH='' cd -- "$(dirname -- "$0")/migrations" && pwd)
plan=$(mktemp)
trap 'rm -f "$plan"' EXIT
sum() { shasum -a 256 "$1" | cut -d' ' -f1; }
{
  echo "SELECT 1 AS migration_locked FROM pg_advisory_lock(hashtext('migrations.applied')) \\gset"
  echo "SELECT to_regclass('migrations.applied') IS NULL AS migration_unrecorded,EXISTS(SELECT 1 FROM pg_tables WHERE schemaname='public') AS migration_populated \\gset"
  echo "\\if :migration_unrecorded"
  echo "BEGIN;"
  echo "CREATE SCHEMA IF NOT EXISTS migrations;"
  echo "CREATE TABLE migrations.applied (file text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now(), how text NOT NULL CHECK (how IN ('applied','adopted')));"
  echo "\\if :migration_populated"
  if [ -n "${MIGRATION_ADOPT:-}" ]; then
    while IFS='|' read -r file condition; do
      [ -f "$dir/$file" ] || { echo "MIGRATION_ADOPT names $file, which is not a migration" >&2; exit 1; }
      checksum=$(sum "$dir/$file")
      echo "INSERT INTO migrations.applied(file,checksum,how) SELECT '$file','$checksum','adopted' WHERE $condition;"
    done < "$MIGRATION_ADOPT"
  else
    echo "DO \$\$ BEGIN RAISE EXCEPTION 'This database already has tables but no migration ledger; record what it has with MIGRATION_ADOPT.'; END \$\$;"
  fi
  echo "\\endif"
  echo "COMMIT;"
  echo "\\endif"
  for path in "$dir"/[0-9]*.sql; do
    file=$(basename "$path")
    checksum=$(sum "$path")
    echo "SELECT NOT EXISTS(SELECT 1 FROM migrations.applied WHERE file='$file') AS migration_pending \\gset"
    echo "\\if :migration_pending"
    echo "\\echo Applying $file"
    echo "BEGIN;"
    # The file's own transaction lines are replaced by this one, which also records it.
    sed -e '/^BEGIN;$/d' -e '/^COMMIT;$/d' "$path"
    echo "INSERT INTO migrations.applied(file,checksum,how) VALUES('$file','$checksum','applied');"
    echo "COMMIT;"
    echo "\\else"
    echo "DO \$\$ BEGIN IF EXISTS(SELECT 1 FROM migrations.applied WHERE file='$file' AND checksum<>'$checksum') THEN RAISE EXCEPTION '$file changed after it was applied; add a new migration instead.'; END IF; END \$\$;"
    echo "\\endif"
  done
  if [ -n "${MIGRATION_GRANT_TO:-}" ]; then
    echo "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO \"$MIGRATION_GRANT_TO\";"
    echo "GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO \"$MIGRATION_GRANT_TO\";"
  fi
} > "$plan"
psql "$DATABASE_URL" -X -q -v ON_ERROR_STOP=1 -f "$plan"

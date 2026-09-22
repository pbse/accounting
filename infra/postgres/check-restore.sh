#!/bin/sh
# Local-only, synthetic restore rehearsal. Never starts an API or worker.
set -eu
case "${PGHOST:-}" in localhost|127.0.0.1|::1) ;; *) echo 'Set PGHOST to an explicit loopback host; hosted databases are not supported.' >&2; exit 1;; esac
: "${PGPORT:?Set the local PostgreSQL port}"
: "${PGUSER:?Set the local PostgreSQL administrative user}"
command -v psql >/dev/null
command -v pg_dump >/dev/null
command -v pg_restore >/dev/null
root=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
scratch=$(mktemp -d)
source_db="accounting_restore_source_$$"
target_db="accounting_restore_target_$$"
source_created=false
target_created=false
cleanup() {
  if [ "$target_created" = true ]; then dropdb --if-exists "$target_db"; fi
  if [ "$source_created" = true ]; then dropdb --if-exists "$source_db"; fi
  rm -f "$scratch/synthetic.dump"
  rmdir "$scratch"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
createdb "$source_db"
source_created=true
createdb "$target_db"
target_created=true
for migration in "$root"/migrations/*.sql; do
  psql -X -d "$source_db" -v ON_ERROR_STOP=1 -f "$migration" >/dev/null
done
psql -X -d "$source_db" -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
INSERT INTO organizations(id,name) VALUES('org_RestoreA','Synthetic restore A'),('org_RestoreB','Synthetic restore B');
INSERT INTO client_closes(organization_id,entity,period) VALUES('org_RestoreA','Same synthetic client','2026-09'),('org_RestoreB','Same synthetic client','2026-09');
INSERT INTO evidence(close_id,name,digest,content,created_by)
 SELECT id,'synthetic.txt',encode(sha256(convert_to('Synthetic restore evidence','UTF8')),'hex'),'Synthetic restore evidence','restore-rehearsal' FROM client_closes;
INSERT INTO preparation_jobs(close_id,digest,plans,state,created_by)
 SELECT close_id,'intake:'||id,'[]','waiting_evidence','restore-rehearsal' FROM evidence;
INSERT INTO preparation_intakes(job_id,source_id)
 SELECT j.id,e.id FROM preparation_jobs j JOIN evidence e ON e.close_id=j.close_id;
INSERT INTO close_preparations(close_id,inventory_digest,scope,actor)
 SELECT j.close_id,'synthetic-scope',jsonb_build_array(jsonb_build_object('job_id',j.id::text,'source_id',e.id::text,'digest',e.digest,'name',e.name)),'restore-rehearsal' FROM preparation_jobs j JOIN evidence e ON e.close_id=j.close_id;
SQL
pg_dump --format=custom --no-owner --no-acl --file="$scratch/synthetic.dump" "$source_db"
pg_restore --exit-on-error --no-owner --no-acl --dbname="$target_db" "$scratch/synthetic.dump"
psql -X -d "$target_db" -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
DO $$ BEGIN
 IF (SELECT count(*) FROM client_closes WHERE entity='Same synthetic client')<>2
 OR (SELECT count(DISTINCT organization_id) FROM client_closes)<>2
 OR (SELECT count(*) FROM evidence WHERE digest=encode(sha256(convert_to(content,'UTF8')),'hex'))<>2
 OR (SELECT count(*) FROM preparation_jobs WHERE state='waiting_evidence')<>2
 OR (SELECT count(*) FROM preparation_intakes i JOIN preparation_jobs j ON j.id=i.job_id JOIN evidence e ON e.id=i.source_id WHERE j.close_id=e.close_id)<>2
 OR (SELECT count(*) FROM close_preparations g JOIN preparation_jobs j ON j.close_id=g.close_id WHERE g.scope->0->>'job_id'=j.id::text)<>2
 THEN RAISE EXCEPTION 'Synthetic restore readback failed'; END IF;
END $$;
SQL
printf '%s\n' 'PASS: schema, two isolated firm owners, source hashes, pending jobs and preparation scope restored. No workers or providers were started.'

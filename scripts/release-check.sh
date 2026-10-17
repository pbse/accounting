#!/usr/bin/env bash
# Runs a revision's full backend test suite (default HEAD) in the release image's environment: the Dockerfile's
# release-test stage (the runtime's Debian release, poppler and tesseract) against a disposable PostgreSQL 18, production's
# major version, migrated twice. The revision is exported with git archive and mounted read-only, so uncommitted changes
# never count. See docs/operations.md#release-revision-check. Needs Docker and psql; touches no hosted service and creates
# only its own container, network, image and Go cache volumes.
set -euo pipefail
root=$(git rev-parse --show-toplevel)
revision=$(git -C "$root" rev-parse --verify "${1:-HEAD}^{commit}")
port=${RELEASE_CHECK_PG_PORT:-55499}
name=close-release-check-$$
tree=$(mktemp -d)
cleanup() {
  docker rm -f "$name-pg" >/dev/null 2>&1 || true
  docker network rm "$name" >/dev/null 2>&1 || true
  rm -rf "$tree"
}
trap cleanup EXIT
git -C "$root" archive "$revision" | tar -x -C "$tree"

docker network create "$name" >/dev/null
docker run -d --name "$name-pg" --network "$name" -p "127.0.0.1:$port:5432" -e POSTGRES_USER=close -e POSTGRES_PASSWORD=test-only -e POSTGRES_DB=close_release \
  postgres:18 -c fsync=off -c synchronous_commit=off >/dev/null
for _ in $(seq 1 60); do
  docker exec "$name-pg" pg_isready -U close -d close_release >/dev/null 2>&1 && break
  sleep 1
done

docker build --quiet --target release-test -t close-release-test "$tree/services/backend" >/dev/null

host_url="postgres://close:test-only@127.0.0.1:$port/close_release?sslmode=disable"
DATABASE_URL=$host_url bash "$tree/infra/postgres/migrate.sh" >/dev/null
DATABASE_URL=$host_url bash "$tree/infra/postgres/migrate.sh" >/dev/null

run() {
  docker run --rm --network "$name" -v "$tree:/repo:ro" -v close-release-gomod:/go/pkg/mod -v close-release-gocache:/root/.cache/go-build \
    -e TEST_DATABASE_URL="postgres://close:test-only@$name-pg:5432/close_release?sslmode=disable" close-release-test "$@"
}
echo "revision $revision"
echo "postgres $(docker exec "$name-pg" psql -U close -d close_release -Atc 'show server_version')"
run sh -c 'echo "go $(go env GOVERSION)"; node --version; pdftotext -v 2>&1 | head -1; tesseract --version 2>&1 | head -1'
status=0
run || status=$?
if [ "$status" -eq 0 ]; then echo "release check passed for $revision"; else echo "release check FAILED for $revision" >&2; fi
exit "$status"

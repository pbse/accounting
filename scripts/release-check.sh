#!/usr/bin/env bash
# Runs the backend's full test suite for the checked-out revision in the release image's environment: the Dockerfile's
# release-test stage (the runtime's Debian release, poppler and tesseract) against a disposable PostgreSQL 18, production's
# major version, migrated twice. See docs/operations.md#release-revision-check. Needs Docker and psql; touches no hosted
# service and creates only its own container, network and image.
set -euo pipefail
root=$(git rev-parse --show-toplevel)
if [ -n "$(git -C "$root" status --porcelain -- services/backend infra/postgres)" ]; then
  echo "Commit or stash the backend and migration changes first: this check is for a revision." >&2
  exit 1
fi
revision=$(git -C "$root" rev-parse HEAD)
port=${RELEASE_CHECK_PG_PORT:-55499}
name=close-release-check-$$
cleanup() {
  docker rm -f "$name-pg" >/dev/null 2>&1 || true
  docker network rm "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker network create "$name" >/dev/null
docker run -d --name "$name-pg" --network "$name" -p "127.0.0.1:$port:5432" -e POSTGRES_USER=close -e POSTGRES_PASSWORD=test-only -e POSTGRES_DB=close_release \
  postgres:18 -c fsync=off -c synchronous_commit=off >/dev/null
for _ in $(seq 1 60); do
  docker exec "$name-pg" pg_isready -U close -d close_release >/dev/null 2>&1 && break
  sleep 1
done

docker build --quiet --target release-test -t "close-release-test:$revision" "$root/services/backend" >/dev/null

host_url="postgres://close:test-only@127.0.0.1:$port/close_release?sslmode=disable"
DATABASE_URL=$host_url bash "$root/infra/postgres/migrate.sh" >/dev/null
DATABASE_URL=$host_url bash "$root/infra/postgres/migrate.sh" >/dev/null

network_url="postgres://close:test-only@$name-pg:5432/close_release?sslmode=disable"
echo "revision $revision"
echo "postgres $(docker exec "$name-pg" psql -U close -d close_release -Atc 'show server_version')"
docker run --rm "close-release-test:$revision" sh -c 'echo "go $(go env GOVERSION)"; pdftotext -v 2>&1 | head -1; tesseract --version 2>&1 | head -1'
status=0
docker run --rm --network "$name" -e TEST_DATABASE_URL="$network_url" -e DATABASE_URL="$network_url" "close-release-test:$revision" || status=$?
if [ "$status" -eq 0 ]; then echo "release check passed for $revision"; else echo "release check FAILED for $revision" >&2; fi
exit "$status"

#!/usr/bin/env bash
#
# Schema-baseline equivalence evidence. Applies the migration set of a reference
# git ref and the working tree's migration set to two fresh keyspaces on a
# disposable Cassandra, then requires identical system_schema rows and seeded
# data (internal/db/schema_baseline_equivalence_test.go).
#
#   ./scripts/schema-baseline-equivalence.sh               # reference: origin/main
#   ./scripts/schema-baseline-equivalence.sh <git-ref>     # any commit/branch/tag
#
# Use it whenever migrations are folded into the initial schema: the fold is
# correct only if this reports no difference against the pre-fold ref.
#
# Needs docker only. The Cassandra container and network are created here and
# removed on exit; no running stack or keyspace is touched.
#   CASSANDRA_IMAGE (default cassandra:5.0.9, the docker-compose.yaml image)
#   GO_IMAGE        (default golang:1.25-trixie, the Dockerfile.gotest base)

set -euo pipefail
cd "$(dirname "$0")/.."
# Git Bash would rewrite the container paths below into Windows host paths.
export MSYS_NO_PATHCONV=1

REF="${1:-origin/main}"
CASSANDRA_IMAGE="${CASSANDRA_IMAGE:-cassandra:5.0.9}"
GO_IMAGE="${GO_IMAGE:-golang:1.25-trixie}"
NAME="sesamefs-schema-equiv-$$"
WORK="$(mktemp -d)"

cleanup() {
  docker rm -f "$NAME-cassandra" >/dev/null 2>&1 || true
  docker network rm "$NAME" >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT

git rev-parse --verify --quiet "$REF^{commit}" >/dev/null || {
  echo "unknown git ref: $REF" >&2
  exit 2
}
echo "reference: $REF ($(git rev-parse --short "$REF^{commit}"))"

# Reference migrations exactly as committed at REF, current tree as checked out.
mkdir -p "$WORK/src/reference"
git archive "$REF" internal/db/migrations | tar -x -C "$WORK/src/reference"
tar -cf - go.mod go.sum cmd internal | tar -x -C "$WORK/src"

docker network create "$NAME" >/dev/null
docker run -d --name "$NAME-cassandra" --network "$NAME" --network-alias cassandra \
  -e MAX_HEAP_SIZE=1G -e HEAP_NEWSIZE=256M "$CASSANDRA_IMAGE" >/dev/null

# The source travels over stdin so no host path is bind-mounted (Git Bash on
# Windows rewrites -v paths). The test retries the connection while Cassandra boots.
tar -C "$WORK/src" -cf - . | docker run --rm -i --network "$NAME" \
  -v sesamefs-schema-equiv-gomod:/go/pkg/mod \
  -e SESAMEFS_SCHEMA_EQUIV_HOSTS=cassandra \
  -e SESAMEFS_SCHEMA_EQUIV_REFERENCE_DIR=/src/reference/internal/db/migrations \
  "$GO_IMAGE" sh -c 'mkdir -p /src && tar -x -C /src && cd /src &&
    go test -tags schemaequiv -run "^TestSchemaBaselineEquivalence$" -count=1 -v ./internal/db'

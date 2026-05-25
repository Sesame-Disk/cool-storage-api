---
name: Always run integration tests via Docker Compose profiles
description: The correct test commands for this project — never run go test directly from host
type: feedback
---

Run integration tests via Docker Compose `--profile test`, NOT directly with `go test` from the host (Cassandra resolves as `cassandra:9042` inside Docker, not from host).

**Why:** Running tests from the host fails with "failed to resolve cassandra:9042" — all integration tests need to run inside the Docker network where Cassandra is reachable.

**How to apply:** Always use one of these two commands:

- Integration tests only:
  `ENV_FILE=.env.example docker compose --profile test run --rm --build go-integration-test`

- Full test suite (unit + integration + API + OIDC):
  `ENV_FILE=.env.example docker compose --profile test run --rm --build go-all-test`

These commands: build the Go test image, wait for sesamefs + sesamefs-node-2 + sesamefs-node-3 to be healthy, then run tests with SESAMEFS_URL=http://sesamefs:8080, SESAMEFS_URL_2=http://sesamefs-node-2:8080, SESAMEFS_URL_3=http://sesamefs-node-3:8080 inside the container network.

Never invent other test commands or run `go test -tags integration` directly.

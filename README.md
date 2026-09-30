# Unsolved

A place to post the operational problems still done by hand, described as the
process that runs today rather than as a build request. Problem and solution
revisions compete by votes, and a solution can be "don't automate".

Go, chi, Postgres 16 + pgvector (pgx, sqlc, goose), River jobs, templ + htmx,
Tailwind. One server binary; deployed to Fly.io with Neon Postgres.

## Local development

Needs Go (the toolchain in `go.mod` is fetched automatically) and Docker.

```sh
docker compose up -d     # Postgres 16 + pgvector on :5432
cp .env.example .env     # local config; provider keys can stay placeholders
make tools               # sqlc, templ, staticcheck, air, Tailwind CLI
make migrate             # apply migrations
make seed                # load the five seed problems (idempotent)
make dev                 # templ watch + Tailwind watch + air → http://localhost:8080
```

To use `/admin` locally, put your handle in `ADMIN_HANDLES` in `.env`.

## Checks

```sh
make check   # go vet, staticcheck, sqlc diff, templ generate check, go test ./...
```

Tests run against a real Postgres started with testcontainers
(`pgvector/pgvector:pg16`), so Docker must be running. After editing `.sql`
or `.templ` files run `make generate` and commit the generated code.

## Layout

- `cmd/server`, `cmd/migrate`, `cmd/seed`: the binaries
- `internal/http`: router and handlers → `internal/service`: rules → `internal/store`: sqlc queries (`internal/store/queries/*.sql`)
- `internal/jobs`: River workers; `internal/views`: templ components
- `migrations/`: goose SQL migrations

The spec is [`docs/BUILD.md`](docs/BUILD.md). Deploys, backups, alerts and
other runbooks are in [`docs/OPERATIONS.md`](docs/OPERATIONS.md).

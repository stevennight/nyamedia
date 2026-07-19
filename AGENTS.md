# NyaMedia Contributor Guide

## Project shape

NyaMedia is a single Go service with an embedded React admin UI. The service
manages media providers, libraries, scans, STRM outputs, filesystem webhooks,
and Emby proxying. PostgreSQL is the supported runtime database.

## Repository layout

- `cmd/server`: application entry point.
- `cmd/migrate-sqlite-to-postgres`: one-off SQLite-to-PostgreSQL migration tool.
- `internal/app`: HTTP handlers, business workflows, schedulers, watchers, and
  scan orchestration.
- `internal/storage`: PostgreSQL repositories and embedded SQL migrations.
- `internal/provider`: provider implementations and the shared provider
  interface.
- `internal/model`: domain entities.
- `internal/config`: YAML loading, defaults, and validation.
- `internal/web`: embedded admin UI assets. Generated files under
  `internal/web/static/` are ignored by Git.
- `web`: Vite/React admin UI source.
- `configs/bootstrap.example.yaml`: example runtime configuration.
- `docs`: product, data-model, and scan-flow notes.

## Build, test, and run

Use Go 1.25 and Node 22, matching the Dockerfile.

```powershell
# Backend tests
go test ./...

# Frontend dependency install and production build
Set-Location web
npm ci
npm run build

# Return to the repository root before Go build commands
Set-Location ..
go build ./cmd/server

# Full local stack
docker compose up --build
```

The Vite build writes to `internal/web/static/`, which Go embeds at build time.
Do not hand-edit or commit generated assets. On a fresh checkout, run the
frontend build before compiling the server if the embedded asset directory is
absent or stale.

The frontend currently has no separate lint or test script; use `npm run build`
as its baseline verification. Run `gofmt` on every changed Go file, then run
the focused package tests and `go test ./...` when practical.

## Implementation rules

- Keep HTTP routing and request/response conventions consistent with
  `internal/app`; the service uses the standard library `net/http` stack.
- Keep business logic in `internal/app`, persistence in `internal/storage`,
  and provider-specific behavior in `internal/provider`.
- Preserve the frontend base path `/admin/static/` unless the backend static
  route is changed in the same work.
- Prefer focused tests next to the affected Go package. Update the React page
  and API client together when an API contract changes.
- Treat `data/`, `configs/bootstrap.yaml`, provider credentials, webhook
  tokens, and database URLs as local runtime state. Do not commit secrets or
  generated runtime data.

## Database migrations

Migrations in `internal/storage/migrations/` are embedded and applied during
service startup. Add a new, zero-padded migration using the next numeric
prefix; do not modify an already released migration. Keep migrations safe for
existing databases and include a repository or application test when behavior
changes.

## Change checklist

- Format touched Go files with `gofmt`.
- Build the frontend after UI changes.
- Run relevant Go tests; run `go test ./...` for backend-wide changes.
- Review `git diff --check` before handing off.

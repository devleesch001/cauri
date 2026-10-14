# AGENTS.md

Instructions for coding agents working on cauri. `CLAUDE.md` is a symbolic
link to this file. Read `CONTEXT.md` for the original brief, the decisions
taken and why; `README.md` for users and operators.

## What cauri is

A visualisation tool for the Cowrie SSH honeypot, built from scratch
(Honeywatch is only a reference for the data model, never a code base to
copy). Three parts in one Go module and one Docker image:

- **ingestor**: reads what Cowrie writes on disk and forwards it, raw;
- **API** (Go, chi): receives ingestor data, stores it, serves the UI;
- **UI** (Vue 3, Vite, TypeScript, Pinia, Vue Router, OpenVue).

Long-term goals: attack statistics and session replay with
asciinema-player. Version 1 lays the foundations; never make these two
harder.

## Invariants

Do not break these without the user's explicit agreement.

**One binary, one image.** Roles are subcommands of `cmd/cauri`: `api`,
`api --ui`, `ui`, `ingestor`, `token`, `version`. A future all-in-one
binary is a build tag (`allinone`), not a fork.

**The ingestor computes nothing.** It sends each `cowrie.json` line
unparsed, plus the sensor id and an event id (`protocol.EventID`: SHA-256
of sensor id and line). It may only read what it needs to act:
`cowrie.log.closed` to upload the TTY recording. All interpretation of
Cowrie events happens in the API.

**One ingestion service.** Validation, deduplication, projection into
tables and recording storage live in `internal/ingest.Service`. HTTP
handlers only translate requests and map errors; the future all-in-one
mode calls the service directly. The ingestor writes to the `Sink`
interface (`internal/ingestor/sink.go`); HTTPS is its only implementation.

**Public protocol in `pkg/`.** `pkg/protocol` (routes, payloads, ids,
digests) and `pkg/client` are the contract third-party tools may use.
`internal/` is only for internal logic. Changing `pkg/` is changing a
public API: keep it backward compatible or say so.

**At-least-once delivery.** The ingestor saves its position only after the
API acknowledged a batch; `cowrie.json` and its rotations are the buffer.
Event batches are never dropped. The API must stay idempotent: resending
anything (events, recordings) must not change the result.

**Files are identified by inode and fingerprint, not by name** (rotation
renames `cowrie.json` to `cowrie.json.YYYY-MM-DD`).

**Database.** SQLite (`modernc.org/sqlite`, no CGO) or PostgreSQL, chosen
by configuration, through bun. SQL must run unchanged on both: no
auto-increment, no PostgreSQL-only features, no SQLite-only functions
(use `CASE`, not `MIN(a,b)`/`LEAST`). Ids are computed by the application.
Times are stored in UTC (SQLite compares them as text). The API is the
only writer, except `cauri token`, accepted by the user.

**Migrations.** goose, SQL files embedded from
`internal/store/migrations`, applied when the API starts. Released
migrations are immutable: add a new file.

**TTY recordings** are stored raw in a volume, named by the SHA-256 of
the file (`internal/ttystore`); the database keeps the reference.
Conversion to asciicast will happen at read time
(`/api/sessions/{id}/cast`), never at ingestion.

**Same origin.** The UI calls relative `/api/...` paths: no CORS, no API
URL in the UI. The reverse proxy routes `/api` to the API.

**Authentication.** Ingestors: one revocable token per sensor, bearer
header, only its hash stored, bound to its sensor id. UI and read API: none
yet; it plugs in as the `ReadAuth` middleware of `internal/api`, without
restructuring.

**Deployment.** Never invent values of the user's setup (domains, paths,
networks, passwords, runner labels): use documented environment variables
or ask. Containers run as non-root. Traefik is the example proxy of the
compose files, not a requirement, and no proxy middleware may be made
mandatory.

## Repository

| Path | Content |
|---|---|
| `cmd/cauri/` | Subcommands; flags via `internal/envflag` (flag `--db-dsn` = env `CAURI_DB_DSN`) |
| `pkg/protocol/`, `pkg/client/` | Public ingestion protocol and its HTTP client |
| `internal/ingest/` | Ingestion service; Cowrie parsing in `cowrie.go`, projection in `batch.go` |
| `internal/store/` | bun models, migrations, read queries; `storetest/` gives test databases |
| `internal/ttystore/` | Content-addressed recording storage |
| `internal/auth/` | Ingestor tokens |
| `internal/api/` | chi router, ingestion and read handlers, middlewares |
| `internal/ingestor/` | Ingestor loop, TTY queue, `Sink`; `tail/` follows the log files |
| `internal/webui/` | Serves the UI embedded from `internal/webui/dist` |
| `ui/` | Vue app; builds into `../internal/webui/dist`; `ui/go.mod` keeps `node_modules` out of the Go module |
| `deploy/` | Compose examples (mode 1: api/ui/ingestor; mode 2: api --ui/ingestor), `.env.example` |
| `.forgejo/workflows/ci.yml` | CI: tests, lint, UI build, image push to the Forgejo registry |

## Commands

Full development setup (local Cowrie, API, ingestor and UI dev server,
PostgreSQL, troubleshooting): `docs/development.md`.

```sh
make test                      # go test ./... (SQLite)
make lint                      # golangci-lint (config: .golangci.yml)
make ui                        # npm ci && npm run build in ui/
make build                     # bin/cauri, static, UI embedded if built

# PostgreSQL too: every store/ingest/api/auth test then runs on both.
docker run -d --rm -p 127.0.0.1:55432:5432 -e POSTGRES_PASSWORD=test postgres:17-alpine
CAURI_TEST_PG_DSN='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable' go test -race ./...

cd ui && npm run lint && npm run typecheck && npm run build
cd ui && npm run dev           # proxies /api to localhost:8080 (CAURI_API_URL to change)

docker build -t cauri:dev .
```

Before considering a change done: `gofmt -l .` empty, `go vet ./...`,
race tests on SQLite and PostgreSQL, `golangci-lint run ./...`, and for UI
changes lint, typecheck and build. For visible UI changes, look at the
result in a browser.

## Conventions

**Go.** Standard library first; `log/slog` (JSON); errors wrapped with
`%w`, sentinel errors for the cases callers map (see `internal/ingest`).
Match the surrounding comment density: comments say why, not what.
Tests that touch the database use `storetest.Run` so they cover both
engines. API tests go through `pkg/client`, which also checks the public
protocol. Ingestor tests use a fake `Sink`; `e2e_test.go` runs the real
chain.

**Cowrie input is hostile.** Attackers control usernames, passwords,
commands and URLs; a compromised Cowrie controls its files. Keep:
`cleanText` (no NUL or invalid UTF-8 reaches PostgreSQL), lenient
per-field parsing (a wrong type empties a column, never drops the event),
`os.Root` and regular-file checks for TTY files, ignored symlinks in the
log directory, size limits (`MaxBatchBytes`, `max-tty-size`), no
`v-html` in the UI.

**Real Cowrie behaviour** (verified on the official image v3.1.2, sample
in `internal/ingest/testdata/cowrie-v3.1.2.json`):
- durations are `duration_ms`; older versions wrote `duration` in seconds;
- a recording's file name and `shasum` hash the terminal output, not the
  file: `sessions.tty_log` keeps Cowrie's name, `tty_sha256`/`tty_size`
  come only from the uploaded file;
- files are written by uid 999 with umask 0022 (readable by the ingestor's
  own non-root user).
When adding support for an event, check it against real Cowrie output.

**UI.** Components from OpenVue only (`openvue/<component>`, themes from
`@openvue/themes`, icons from `@openvue/icons`); do not switch to PrimeVue
without asking. Every user-facing string goes through vue-i18n
(`ui/src/locales/en.ts` is the typed reference). Filters live in the URL
query. Visual system in `ui/src/style.css`: IBM Plex Sans for the UI, IBM
Plex Mono only for technical values; red (`--c-shell`) means only "an
attacker obtained a shell". Respect dark mode, reduced motion, keyboard
focus, phone widths.

**CI.** Third-party actions are pinned to commit SHAs with the version in
a comment; jobs run on `ubuntu-latest`.

## Way of working

- For a new feature or an architecture change: present a plan (files,
  schema, protocol, steps) and wait for the user's agreement before
  writing code.
- Ask rather than guess when a choice belongs to the user; say so when a
  simpler option exists.
- One commit per coherent step, message in English: a short imperative
  subject, then why.
- Report what was verified and what was not (for example, the CI cannot
  run locally).
- The user writes in French; code, comments, UI texts and docs are in
  English.

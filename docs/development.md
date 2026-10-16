# Development environment

How to set up a machine to work on cauri, run the whole chain locally and
check a change before committing. Rules and conventions for the code are
in [`AGENTS.md`](../AGENTS.md); the reasons behind the design in
[`CONTEXT.md`](../CONTEXT.md).

## How it fits together

In development, each part runs on its own, from the source tree, and a
real Cowrie runs in Docker:

```
Cowrie (Docker, SSH on 127.0.0.1:22222) ──► data/cowrie/   (cowrie.json, TTY recordings)
                                                 │
Terminal 2: ingestor (go run) ───────────────────┘ reads data/cowrie, sends to ↓
Terminal 1: API      (go run, :8090) ──► data/cauri.db, data/tty
Terminal 3: UI       (Vite, :5173)   ──► calls /api, proxied to the API
```

Everything the tools write goes to `data/`, ignored by git. You open the
UI on the Vite dev server (http://localhost:5173): it reloads on every
save and forwards `/api` to the API.

The API listens on **8090** in this guide rather than the default 8080,
which other tools often take. Any free port works: change `CAURI_LISTEN`
and the two URLs that point at it.

## Prerequisites

| Tool                | Version                  | Used for                      |
|---------------------|--------------------------|-------------------------------|
| Go                  | 1.27.2 (see `go.mod`)    | API, ingestor, tests          |
| Node.js and npm     | 24                       | UI                            |
| Docker with Compose | recent                   | Cowrie, the image             |
| golangci-lint       | v2.14.0 (same as the CI) | Go lint                       |
| git, make           | any                      |                               |

No C compiler is needed: the SQLite driver is pure Go. The race detector (`go test -race`) does need one (gcc or clang).

```sh
go version && node --version && docker compose version && golangci-lint version
```

The project uses Go 1.27.2 everywhere: `go.mod` requires it, so an older
`go` command downloads and uses it automatically in the project
(`GOTOOLCHAIN=auto`, the default); the CI installs that version and the
Dockerfile builds with it.

golangci-lint must be v2.14.0 or later: Go 1.27.2 compiles packages in a
newer format (export data version 5) that earlier versions cannot read,
and they fail with "export data version 5 is greater than maximum
supported version 4".

## First setup

Once per clone:

```sh
git clone <repository URL> cauri && cd cauri
make init-dev
```

`make init-dev` checks the tools, installs the UI dependencies, prepares
and starts a local Cowrie, and creates the dev token. It can be run again
at any time: it keeps what exists (the token, Cowrie's data) and restarts
Cowrie if it was stopped. The two sections below explain what it does.

Then check that everything builds and passes:

```sh
make test && make lint
cd ui && npm run lint && npm run typecheck && cd ..
```

### A local Cowrie

`deploy/compose.dev.yml` runs the official Cowrie image, its `var`
directory mounted on `data/cowrie`. Two things to know:

- Cowrie does not create its log and TTY directories: create them before
  the first start, or it writes no `cowrie.json` and fails when a shell
  opens.
- It runs under your user (`DEV_UID`/`DEV_GID`), so the files it writes
  are yours and can be deleted without root.

```sh
export DEV_UID=$(id -u) DEV_GID=$(id -g)     # needed by every compose.dev.yml command
mkdir -p data/cowrie/log/cowrie data/cowrie/lib/cowrie/tty data/cowrie/lib/cowrie/downloads
docker compose -f deploy/compose.dev.yml up -d cowrie
```

SSH is on `127.0.0.1:22222` (`COWRIE_DEV_PORT` to change it), so it does
not clash with a Cowrie already running on 2222. `make dev-down` stops
it.

### An ingestor token

The API creates its database on first use, so this works before the API
ever ran (`make init-dev` does it when `data/dev.token` is missing):

```sh
go run ./cmd/cauri token create --sensor dev > data/dev.token
```

## Daily work: three terminals

```sh
make dev-api         # terminal 1: API on :8090
make dev-ingestor    # terminal 2: reads data/cowrie, sends to the API
make dev-ui          # terminal 3: UI on http://localhost:5173
```

In VS Code, the task "dev: all" starts the three, or "Full stack" in Run
and Debug starts them under the debugger (see [VS Code](#vs-code)).

Open http://localhost:5173. The ingestor may log `log directory does not
exist yet`: it waits, and starts reading as soon as Cowrie writes.

The targets run these commands. Their settings can be changed on the
command line, for example `make dev-api DEV_API_PORT=8091`; pass the same
value to the three targets.

```sh
# make dev-api
CAURI_LISTEN=:8090 go run ./cmd/cauri api --log-level debug

# make dev-ingestor
go run ./cmd/cauri ingestor --api-url http://localhost:8090 \
  --token-file data/dev.token --sensor-id dev \
  --cowrie-log-dir data/cowrie/log/cowrie \
  --cowrie-tty-dir data/cowrie/lib/cowrie/tty \
  --state-dir data/ingestor --log-level debug

# make dev-ui (Vite uses port 5173 only and stops if it is taken)
cd ui && CAURI_API_URL=http://localhost:8090 npm run dev
```

### Producing data

Play the attacker against your Cowrie (default accounts of the image):

```sh
ssh -p 22222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null admin@127.0.0.1
# password "admin": a shell opens; type commands, then exit

ssh -p 22222 -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null root@127.0.0.1
# password "123456": refused, a failed login
```

The sessions appear in the UI a second or two later, with their
recording once the shell is closed (only for a session with a single
channel: bots running a command per channel are replayed, simulated, from
their commands).

### Without Docker: replaying a capture

To work on the UI without Cowrie, feed the API with the capture of the
real Cowrie kept in the tests (4 sessions; its recordings are not part of
it, so the ingestor logs that it drops them):

```sh
mkdir -p data/sample/log data/sample/tty
cp internal/ingest/testdata/cowrie-v3.1.2.json data/sample/log/cowrie.json
go run ./cmd/cauri token create --sensor sample > data/sample.token
go run ./cmd/cauri ingestor --api-url http://localhost:8090 \
  --token-file data/sample.token --sensor-id sample \
  --cowrie-log-dir data/sample/log --cowrie-tty-dir data/sample/tty \
  --state-dir data/sample/state
```

Stop it with Ctrl+C once the events are sent. Any `cowrie.json` works the
same way; a second sensor id also lets you try the sensor filter.

### What to restart after a change

| You change                                                      | You do                                         |
|-----------------------------------------------------------------|------------------------------------------------|
| Vue or TypeScript code in `ui/`                                 | Nothing: the page reloads                      |
| Go code of the API (`internal/api`, `ingest`, `store`, `auth`…) | Ctrl+C and `make dev-api` again                |
| Go code of the ingestor (`internal/ingestor`)                   | Ctrl+C and `make dev-ingestor` again           |
| The database schema                                             | Add a migration (see below), restart the API   |
| The protocol (`pkg/`)                                           | Restart both; it is public, keep it compatible |

### Starting over

Stop the API and the ingestor, then:

```sh
make dev-reset && make init-dev                   # cauri data only: database, recordings, ingestor state, token
make dev-down && rm -rf data && make init-dev     # everything, Cowrie included
```

Removing only the ingestor state (`data/ingestor`) makes it send
everything again: the API drops the duplicates, which is a quick way to
see deduplication at work.

## Tests

```sh
make test                 # go test ./... on SQLite
go test -race ./...       # what the CI runs
go test ./internal/ingestor/... -run Rotation -v   # one test
```

Tests run on SQLite, the database cauri is built for and the only one the
CI tests. When a change touches SQL (migrations, queries, ingestion), run
them on PostgreSQL too: tests touching the database also run on it when
`CAURI_TEST_PG_DSN` is set, each in its own schema. A throwaway server is
enough (`--rm`: it disappears when stopped):

```sh
docker run -d --rm --name cauri-test-pg -p 127.0.0.1:55432:5432 -e POSTGRES_PASSWORD=test postgres:17-alpine
export CAURI_TEST_PG_DSN='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable'
go test -race ./...
docker stop cauri-test-pg
```

Where tests live:

| Package                                       | What they cover                                                                                                       |
|-----------------------------------------------|-----------------------------------------------------------------------------------------------------------------------|
| `internal/ingest`                             | Ingestion service: projection, deduplication, merge order, hostile strings, TTY storage, the real Cowrie capture      |
| `internal/store`                              | Migrations, time handling, read queries and filters                                                                   |
| `internal/api`                                | HTTP layer, through `pkg/client`                                                                                      |
| `internal/ingestor`, `tail/`                  | Rotation, restart, API down, stable ids, TTY queue, confinement; `e2e_test.go` runs files → ingestor → API → database |
| `internal/auth`, `ttystore`, `webui`, `pkg/*` | Their own unit                                                                                                        |

## Running cauri on PostgreSQL

SQLite is the default; PostgreSQL is an option for users who prefer it.
To try cauri on it, with the throwaway server started as above, give the
same database settings to the API and to `cauri token`:

```sh
export CAURI_DB_DRIVER=postgres
export CAURI_DB_DSN='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable'
go run ./cmd/cauri token create --sensor dev > data/dev.token
CAURI_LISTEN=:8090 go run ./cmd/cauri api --log-level debug
```

## Common tasks

### Changing the database schema

1. Add the migration twice, once per engine, with the same number and
   name: `internal/store/migrations/sqlite/000NN_<what>.tx.up.sql` and
   `.tx.down.sql`, and the same in `postgres/`. Each engine uses its own
   types (see [db.md](db.md#types)); separate statements with a
   `--bun:split` line; no `?` in the SQL (bun reads it as a parameter).
   Never edit a released migration.
2. Times are integer microseconds (`store.Time`), durations integer
   milliseconds, ids generated by the database.
3. Update the bun models in `internal/store/models.go`, then the
   ingestion (`internal/ingest/batch.go`) or the queries (`internal/store/query.go`).
   Queries stay identical on both engines.
4. Run the tests on SQLite and on PostgreSQL: `TestSchemasMatch` fails if
   the two migration sets differ in tables, columns or indexes. The
   migration runs when the API starts.

### Supporting a new Cowrie event

The event is already stored in `events`, its fields in `events.data` and
its line in `event_raw_data`. To project it: read its
fields in `internal/ingest/cowrie.go`, use them in `batch.go`, show it in
the timeline (`ui/src/timeline.ts`, texts in `ui/src/locales/en.ts`).
Check the field names and types against real Cowrie output (your dev
Cowrie writes it in `data/cowrie/log/cowrie/cowrie.json`).

### UI work

- Components come from OpenVue: `import DataTable from 'openvue/datatable'`.
- Every text goes in `ui/src/locales/en.ts` and is used through `t()`;
  `npm run typecheck` fails on an unknown key.
- Colours, fonts and spacing are variables in `ui/src/style.css`.
- `npm run lint`, `npm run typecheck` and `npm run build` must pass.

### The UI served by the binary

The Vite dev server is for development. To check the UI as it ships,
embedded in the binary:

```sh
make ui && make build
CAURI_LISTEN=:8090 ./bin/cauri api --ui     # then http://localhost:8090
```

`go run` does not rebuild the UI: without `make ui`, `/` answers that the
UI is not part of the build.

### The Docker image

```sh
docker build -t cauri:dev .
docker run --rm cauri:dev version
```

To try a deployment mode locally, use the files of `deploy/` with an
`.env` pointing at `cauri:dev`; see the README.

## Before committing

```sh
gofmt -l .                 # must print nothing
go vet ./...
go test -race ./...        # add CAURI_TEST_PG_DSN if the change touches SQL
make lint
cd ui && npm run lint && npm run typecheck && npm run build
```

Commit conventions are in `AGENTS.md`.

## VS Code

The repository has a shared configuration in `.vscode/`; accept the
recommended extensions (Go, Vue, ESLint) when VS Code offers them. The Go
extension installs its tools (gopls, Delve) on first use.

| Where                | Entry                                  | What it does                                                                     |
|----------------------|----------------------------------------|----------------------------------------------------------------------------------|
| Terminal > Run Task  | `dev: init`                            | `make init-dev`                                                                  |
|                      | `dev: api`, `dev: ingestor`, `dev: ui` | One `make dev-*` each, in its own terminal                                       |
|                      | `dev: all`                             | The three together                                                               |
|                      | `dev: stop containers`                 | `make dev-down`                                                                  |
|                      | `check: go`, `check: ui`               | The checks of [Before committing](#before-committing)                            |
| Run and Debug        | `API (debug)`, `Ingestor (debug)`      | Same commands as `make dev-api` and `make dev-ingestor`, under Delve             |
|                      | `UI (Chrome)`                          | Starts `dev: ui` and opens Chrome on it, with breakpoints in Vue and TypeScript  |
|                      | `Full stack`                           | The three; stopping one stops all                                                |

Settings: Go files are formatted and their imports organised on save,
golangci-lint runs on save, ESLint fixes Vue and TypeScript files on
save, and the editor uses the project's TypeScript. To run Go tests from
the editor on PostgreSQL too, uncomment `CAURI_TEST_PG_DSN` in
`.vscode/settings.json`.

The debug configurations repeat the arguments of the Makefile targets:
change both together.

## IDE notes

- `ui/go.mod` is there on purpose: it keeps `ui/node_modules`, where some
  npm packages ship Go files, out of the Go module. An IDE may show `ui/`
  as a second Go module; that is expected.
- Run configurations for the API and the ingestor are the commands of
  this guide: program `./cmd/cauri`, arguments `api` or `ingestor …`,
  environment `CAURI_LISTEN=:8090`.
- ESLint and TypeScript configurations are in `ui/` (`eslint.config.ts`,
  `tsconfig*.json`).

## Troubleshooting

| Symptom                                                                           | Cause and fix                                                                                                                      |
|-----------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------|
| `listen tcp :8090: bind: address already in use` | An API is already running; stop it, or pass `DEV_API_PORT=<port>` to the three `make dev-*` targets |
| `Error: Port 5173 is already in use` | A UI dev server is already running (another terminal, or VS Code): stop it, or use it |
| `Bind for 127.0.0.1:22222 failed: port is already allocated`                      | Set `COWRIE_DEV_PORT` to a free port                                                                                               |
| `required variable DEV_UID is missing`                                            | `export DEV_UID=$(id -u) DEV_GID=$(id -g)` in this shell                                                                           |
| Cowrie logs `No such file or directory: 'var/lib/cowrie/tty/…'`, no `cowrie.json` | Its directories were not created before the start: create them (First setup), restart Cowrie                                       |
| Files in `data/cowrie` belong to uid 999 and cannot be deleted                    | Cowrie was started without `DEV_UID`. Delete them through a container: `docker run --rm -v "$PWD/data:/d" alpine rm -rf /d/cowrie` |
| The ingestor logs `401 Unauthorized` | The token is not in the database the API uses (database reset, other driver): `make init-dev` creates a new one, then restart `make dev-ingestor` |
| The UI shows "The API did not answer"                                             | API not running, or `CAURI_API_URL` of the Vite server does not match `CAURI_LISTEN`                                               |
| `/` of the binary says the UI is not part of the build                            | Run `make ui`, then rebuild                                                                                                        |
| `go test ./...` picks up a package under `ui/node_modules`                        | `ui/go.mod` is missing; restore it                                                                                                 |

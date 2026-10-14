# cauri

cauri collects what the [Cowrie](https://github.com/cowrie/cowrie) SSH
honeypot records, from one or several sensors, and shows it: sessions,
credentials tried, commands typed, files fetched. It keeps the raw terminal
recordings so that sessions can be replayed once replay lands in the UI.

One Go binary and one Docker image provide every role:

| Command | Role |
|---|---|
| `cauri api` | API: receives data from ingestors, serves it to the UI |
| `cauri api --ui` | API that also serves the web UI |
| `cauri ui` | Web UI only |
| `cauri ingestor` | Reads a Cowrie sensor's files and sends them to the API |
| `cauri token` | Creates, lists and revokes ingestor tokens |
| `cauri version` | Prints the version |

## Architecture

```
Cowrie ──► cowrie.json ──┐
       └─► tty/<file>  ──┤
                         ▼
                    Ingestor (follows the log, saves its position,
                         │    buffers, adds the sensor id)
                         │  POST /api/ingest/events     batches of raw events (JSON)
                         │  PUT  /api/ingest/tty/{id}   raw TTY recording, once per session
                         ▼
                        API ──► database (events, sessions, logins, commands, transfers)
                         │  └─► volume (raw TTY recordings)
                         ▼
                        UI: sessions, session detail, failed logins
                            (later: statistics, replay via /api/sessions/{id}/cast)
```

**The ingestor does not interpret events.** It sends each line of
`cowrie.json` as is, with the sensor id and an event id: the SHA-256 of
the sensor id and the line, so that the API can drop events it already
has. When it sees `cowrie.log.closed`, it uploads the matching TTY
recording with its SHA-256.

**The API owns all the logic**, in a single ingestion service
(`internal/ingest`): validation, deduplication, projection of the events
into tables, storage of recordings. The HTTP handlers only translate
requests; a future all-in-one build will call the same service directly.

**Delivery is at least once.** The ingestor saves its read position only
after the API acknowledged a batch. While the API is unreachable it stops
reading and retries; `cowrie.json` itself is the buffer, so nothing is
lost as long as Cowrie's rotated log files are kept until the API is back.
After a restart, or even after losing its state, the ingestor sends lines
again and the API drops the duplicates. Pending recording uploads are kept
in a file of the state directory.

**Rotation.** Cowrie renames `cowrie.json` to `cowrie.json.YYYY-MM-DD`
every day. The ingestor identifies files by inode and by a fingerprint of
their first bytes, not by name: it finishes the renamed file, then moves
to the new one, including across restarts. Compressed rotations (`.gz`…)
and symbolic links are ignored.

### Storage

| Table | Content |
|---|---|
| `raw_events` | Every event as Cowrie wrote it, including fields not used today |
| `sessions` | One row per Cowrie session and sensor: source, client, HASSH, times, login outcome, recording |
| `login_attempts` | Username and password of every login, with its outcome |
| `commands` | Commands typed |
| `transfers` | Downloads and uploads: URL or file name, SHA-256 |
| `tty_files` | Stored recordings, by SHA-256 of the file |
| `sensors`, `ingest_tokens` | Sensors seen, ingestor tokens (hashed) |

Recordings are stored raw, unconverted, in the TTY directory, named by the
SHA-256 of their content; the database only references them. Note that
the name Cowrie gives a recording is a hash of the terminal output, not of
the file: cauri keeps it in `sessions.tty_log` and checks uploads against
the real digest.

The schema is portable: the same SQL runs on SQLite (pure Go driver, no
CGO) and PostgreSQL. Migrations (goose) are embedded and applied when the
API starts.

### Repository

```
cmd/cauri/           subcommands
pkg/protocol/        ingestion protocol: routes, payloads, event id, digest (public)
pkg/client/          HTTP client of the ingestion protocol (public)
internal/ingest/     ingestion service
internal/store/      database: models, migrations, read queries
internal/ttystore/   storage of TTY recordings
internal/auth/       ingestor tokens
internal/api/        HTTP API (chi)
internal/ingestor/   ingestor; tail/ follows the log files
internal/webui/      serves the UI, embedded from internal/webui/dist
ui/                  Vue 3 + Vite + TypeScript + Pinia + OpenVue
deploy/              compose files and .env.example
```

`pkg/` holds what another program needs to feed a cauri API, for example a
custom ingestor or an import tool.

## Configuration

Every option is a flag and an environment variable: `--db-dsn` is
`CAURI_DB_DSN`. A flag given on the command line wins.
`cauri <command> --help` lists them.

### `cauri api` and `cauri ui`

| Variable | Default | Meaning |
|---|---|---|
| `CAURI_LISTEN` | `:8080` | Listen address (also `cauri ui`) |
| `CAURI_DB_DRIVER` | `sqlite` | `sqlite` or `postgres` |
| `CAURI_DB_DSN` | `data/cauri.db` (image: `/data/cauri.db`) | SQLite file path, or `postgres://user:password@host:5432/db?sslmode=disable` |
| `CAURI_TTY_DIR` | `data/tty` (image: `/data/tty`) | Directory of the TTY recordings |
| `CAURI_MAX_TTY_SIZE` | `134217728` (128 MiB) | Largest recording accepted, in bytes |
| `CAURI_UI` | `false` | `true`: same as `api --ui` |
| `CAURI_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` (also `cauri ui`) |

`cauri token` uses `CAURI_DB_DRIVER` and `CAURI_DB_DSN` too.

### `cauri ingestor`

| Variable | Default | Meaning |
|---|---|---|
| `CAURI_API_URL` | required | Base URL of the API, such as `https://cauri.example.org` |
| `CAURI_TOKEN_FILE` | | File holding the token (preferred) |
| `CAURI_TOKEN` | | The token itself |
| `CAURI_SENSOR_ID` | required | Identifier of the sensor: letters, digits, `.`, `_`, `-`; up to 64 |
| `CAURI_COWRIE_LOG_DIR` | required | Cowrie's `var/log/cowrie`, holding `cowrie.json` |
| `CAURI_COWRIE_TTY_DIR` | required | Cowrie's `var/lib/cowrie/tty` |
| `CAURI_STATE_DIR` | `data/ingestor` (image: `/data/ingestor`) | Writable directory: read position, pending uploads |
| `CAURI_START` | `beginning` | Without saved position: `beginning` sends the existing logs, rotated ones included; `end` only new events |
| `CAURI_BATCH_SIZE` | `500` | Events per batch at most |
| `CAURI_FLUSH_INTERVAL` | `1s` | Delay before sending an incomplete batch |
| `CAURI_POLL_INTERVAL` | `500ms` | Delay between checks for new lines |
| `CAURI_HTTP_TIMEOUT` | `1m` | Timeout of a request to the API |
| `CAURI_LOG_LEVEL` | `info` | Log level |

### Tokens

Each ingestor has its own token, bound to its sensor id: an ingestor
cannot send data in the name of another sensor. Only a hash of the token
is stored.

```sh
cauri token create --sensor hp-01 --name "vps-paris"   # prints the token once
cauri token list
cauri token revoke <id>                                # effective immediately
```

With Docker: `docker compose exec api cauri token create --sensor hp-01`.

## Deployment

The image runs as the distroless `nonroot` user (uid 65532). Its `/data`
directory is owned by that user, so a new named volume mounted there is
writable. With a bind mount instead, give the directory to uid 65532.

Copy `deploy/.env.example` to `deploy/.env` and fill it in: domain, Traefik
network, entrypoint and certificate resolver, image, sensor id, Cowrie
directory, token file. The compose files read every site-specific value
from it.

### Cowrie's files

The ingestor mounts, read-only, the host directory behind Cowrie's
`/cowrie/cowrie-git/var` (`COWRIE_VAR_DIR`): it needs
`log/cowrie/cowrie.json` and `lib/cowrie/tty/`. The official image writes
them with umask 0022, so the ingestor can read them under its own user.

If Cowrie keeps `var` in a named volume rather than a host directory,
declare that volume as `external` in the compose file and mount it in
place of `COWRIE_VAR_DIR`.

### Mode 1: three containers

```sh
cd deploy
docker compose -f compose.separate.yml up -d api ui
docker compose -f compose.separate.yml exec api cauri token create --sensor "$CAURI_SENSOR_ID" > <token file>
docker compose -f compose.separate.yml up -d ingestor
```

Traefik routes `Host(CAURI_DOMAIN) && PathPrefix(/api)` to `api` and the
rest of the domain to `ui`: one origin, no CORS, no API URL in the UI.

### Mode 2: API with UI, and ingestor

```sh
cd deploy
docker compose -f compose.api-ui.yml up -d api
docker compose -f compose.api-ui.yml exec api cauri token create --sensor "$CAURI_SENSOR_ID" > <token file>
docker compose -f compose.api-ui.yml up -d ingestor
```

The token file is mounted as a Compose secret. Outside of Swarm, Compose
mounts it with the permissions of the host file: make it readable by uid
65532 (for example `chmod 0444`, in a directory only you can list).

### Remote sensors

On another host, run only the `ingestor` service of either compose file,
with `CAURI_INGEST_API_URL=https://<CAURI_DOMAIN>` and the token of that
sensor. TLS is handled by Traefik in front of the API.

### PostgreSQL

SQLite suits a single host. For PostgreSQL, set
`CAURI_DB_DRIVER=postgres`, `CAURI_DB_DSN` and `POSTGRES_PASSWORD`, then
start with `--profile postgres`, or point `CAURI_DB_DSN` at an existing
server.

### Protecting the UI

**The UI and the read API have no authentication yet.** Until they do,
put them behind protection at Traefik: a `basicAuth` or `forwardAuth`
middleware, or an `ipAllowList`. The compose files give ingestion its own
router (`cauri-ingest`, `/api/ingest`, authenticated by the ingestor
tokens), so a middleware added to the other routers (`cauri-api` and
`cauri-ui` in mode 1, `cauri` in mode 2) does not block ingestors:

```yaml
traefik.http.routers.cauri-ui.middlewares: <your middleware>@<provider>
traefik.http.routers.cauri-api.middlewares: <your middleware>@<provider>
```

In the code, read routes go through a `ReadAuth` middleware
(`internal/api`), a no-op today, where authentication will plug in.

### Backups

Back up the database (the `cauri.db` file of the `cauri-data` volume, or
PostgreSQL) and the TTY directory together. The ingestor state can be
lost: the ingestor then sends everything again and the API drops what it
has.

## Development

```sh
make ui       # npm ci && npm run build in ui/, output in internal/webui/dist
make build    # bin/cauri, static, UI embedded if built
make test
make lint

# PostgreSQL tests in addition to SQLite:
docker run -d --rm -p 127.0.0.1:55432:5432 -e POSTGRES_PASSWORD=test postgres:17-alpine
CAURI_TEST_PG_DSN='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable' make test

# UI with hot reload, against a local "cauri api" (proxy for /api):
cd ui && npm run dev
```

`internal/ingest/testdata` holds a `cowrie.json` written by the official
`cowrie/cowrie` image (v3.1.2); tests check the parsing against it.

CI (`.forgejo/workflows/ci.yml`) runs the same checks and pushes the image
to the registry of the Forgejo instance on `main` and `v*` tags. It needs
a `REGISTRY_TOKEN` secret (token allowed to write packages), optionally a
`REGISTRY_USER` variable, and a runner labelled `docker` whose job
containers can reach a Docker daemon.

## Next steps the code is ready for

- **Replay**: recordings are stored raw; `/api/sessions/{id}/cast` will
  convert them to asciicast on read, for asciinema-player, next to the
  timeline of the session page.
- **Statistics**: queries on the existing tables.
- **UI authentication**: a `ReadAuth` middleware.
- **All-in-one binary** (build tag `allinone`): the ingestor writes to a
  `Sink` interface; an in-process implementation calling
  `ingest.Service` replaces HTTPS.
- **GeoIP**, message broker: not designed in yet, nothing prevents them.

## License

MIT, see [LICENSE](LICENSE).

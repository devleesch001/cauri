# cauri

cauri collects what the [Cowrie](https://github.com/cowrie/cowrie) SSH
honeypot records, from one or several sensors, and shows it: sessions,
credentials tried, commands typed, files fetched. It keeps the raw terminal
recordings and replays them in the browser, at the attacker's real pace.

One Go binary and one Docker image provide every role:

| Command          | Role                                                    |
|------------------|---------------------------------------------------------|
| `cauri api`      | API: receives data from ingestors, serves it to the UI  |
| `cauri api --ui` | API that also serves the web UI                         |
| `cauri ui`       | Web UI only                                             |
| `cauri ingestor` | Reads a Cowrie sensor's files and sends them to the API |
| `cauri token`    | Creates, lists and revokes ingestor tokens              |
| `cauri version`  | Prints the version                                      |

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
                        UI: sessions, session detail and replay, failed logins
                            (replay: GET /api/sessions/{id}/cast, converted on read)
```

**The ingestor does not interpret events.** It sends each line of
`cowrie.json` as is, with the sensor id and an event id: the SHA-256 of
the sensor id and the line, so that the API can drop events it already
has. When it sees `cowrie.log.closed`, it uploads the matching TTY
recording with its SHA-256.

**The API owns all the logic**, in a single ingestion service (`internal/ingest`): validation, deduplication, projection
of the events
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

| Table                      | Content                                                                                       |
|----------------------------|-----------------------------------------------------------------------------------------------|
| `raw_events`               | Every event as Cowrie wrote it, including fields not used today                               |
| `sessions`                 | One row per Cowrie session and sensor: source, client, HASSH, times, login outcome, recording |
| `login_attempts`           | Username and password of every login, with its outcome                                        |
| `commands`                 | Commands typed                                                                                |
| `transfers`                | Downloads and uploads: URL or file name, SHA-256                                              |
| `tty_files`                | Stored recordings, by SHA-256 of the file                                                     |
| `sensors`, `ingest_tokens` | Sensors seen, ingestor tokens (hashed)                                                        |

Recordings are stored raw, unconverted, in the TTY directory, named by the
SHA-256 of their content; the database only references them. Note that
the name Cowrie gives a recording is a hash of the terminal output, not of
the file: cauri keeps it in `sessions.tty_log` and checks uploads against
the real digest.

cauri is built for SQLite first (pure Go driver, no CGO): it is the
default, the configuration tested by the CI, and enough for most
deployments. Through the bun ORM, PostgreSQL is supported too for those
who prefer it: the schema and queries stay portable between both.
Migrations (goose) are embedded and applied when the API starts.

### Repository

```
cmd/cauri/           subcommands
pkg/protocol/        ingestion protocol: routes, payloads, event id, digest (public)
pkg/client/          HTTP client of the ingestion protocol (public)
internal/ingest/     ingestion service
internal/store/      database: models, migrations, read queries
internal/ttystore/   storage of TTY recordings
internal/ttylog/     reads Cowrie's recordings, converts them for replay
internal/asciicast/  asciicast model and encoders (version 3)
internal/verdict/    human/bot verdict of a session
internal/geoip/      GeoIP lookups (MMDB), reloaded when the file changes
internal/auth/       ingestor tokens
internal/api/        HTTP API (chi)
internal/ingestor/   ingestor; tail/ follows the log files
internal/webui/      serves the UI, embedded from internal/webui/dist
ui/                  Vue 3 + Vite + TypeScript + Pinia + OpenVue
deploy/              compose files and .env.example
```

`pkg/` holds what another program needs to feed a cauri API, for example a
custom ingestor or an import tool.

### Replay

`GET /api/sessions/{id}/cast` returns the recording of a session as
[asciicast v3](https://docs.asciinema.org/manual/asciicast/v3/), which the
UI plays with asciinema-player. The stored file is converted on each
request and never modified.

- Real timing is kept. The header carries `idle_time_limit: 2`, a hint
  that makes players shorten pauses longer than 2 s;
  `?idle_time_limit=<seconds>` changes it, `0` removes it (the UI's "Real
  time" switch). The real rhythm tells a human from a bot.
- Terminal output becomes `"o"` events and keystrokes `"i"` events: they
  include what the terminal does not show, such as passwords typed at
  `sudo` or `passwd`.
- The terminal size comes from the client's `cowrie.client.size` events,
  80×24 if none; later changes become `"r"` events.
- Each command typed (`cowrie.command.input`) becomes an `"m"` marker,
  which players show as chapters on the progress bar. Cowrie logs a
  command within a millisecond of its Enter key in the recording.
- `404` if the session has no stored recording, `422` if the file cannot
  be read; an `ETag` makes repeated loads cheap.

`GET /api/sessions/{id}/chapters` returns those commands with what each
printed, as plain text: the terminal output after the echo of the command
line, until the next key typed, without colours, escape sequences or the
final prompt. A full-screen program (an editor, a pager) is flagged
instead of rendered. Output is capped (64 KiB per command, 4 MiB per
session): it comes from the attacker. Both are computed on read, so
sessions recorded before these features have them too.

In the UI, the session page shows the replay with controls (skip 10 or
30 seconds, previous and next command, speed from 0.5× to 8×, real time),
and beside it the session facts, the commands with their output (a click
moves the player there; the current one is highlighted), the logins
tried (passwords and offered public keys) and the files.

### GeoIP

With a GeoIP database, cauri locates each session by its source address:
country and city, shown in the session list (with a country filter), the
failed logins and the session page. A session is located when it is
stored, with the database of that time, which is recorded with it;
sessions stored before a database was configured are located once when
the API starts. Private and reserved addresses have no location.

cauri reads any MMDB file. Two free databases fit; neither is bundled,
and the UI shows the attribution each requires:

| | DB-IP Lite (default choice) | MaxMind GeoLite2 |
|---|---|---|
| Licence | Creative Commons BY 4.0 | MaxMind's GeoLite EULA |
| Account | None | Required (licence key) |
| Obligations | Attribution, shown by the UI | Attribution; use each new version and delete older ones within 30 days |
| Updates | Monthly | Twice a week |

Download DB-IP City Lite (or `dbip-country-lite`, smaller, without
cities), replacing the month:

```sh
curl -fL https://download.db-ip.com/free/dbip-city-lite-2026-10.mmdb.gz | gunzip > geoip.mmdb
```

GeoLite2 is downloaded with MaxMind's `geoipupdate` and your licence key.

Then give the file to the API, `--geoip-db` / `CAURI_GEOIP_DB`. With the
compose files, copy it into the data volume and set
`CAURI_GEOIP_DB=/data/geoip.mmdb`:

```sh
docker compose cp geoip.mmdb api:/data/geoip.mmdb
```

To update, copy the new file over the old one: the API reloads it within
a minute, without restarting. The database is held in memory (about
120 MiB for DB-IP City Lite, 8 MiB for the country version), so replacing
the file in place is safe.

### Human or bot

`GET /api/sessions/{id}/verdict` tells whether a session looks typed by a
human or run by a bot, as `{verdict, score, reasons}`. Each signal adds
points (positive: human); the UI shows the verdict with every reason.

| Signal | Source | Points |
|---|---|---|
| Input sent key by key (≥ 80%) / as whole lines (≤ 20%) | recording | +2 / −2 |
| Irregular (≥ 0.4) / mechanical (< 0.1) intervals between keys | recording | +1 / −2 |
| Human typing speed (median 60–600 ms) / too fast (< 20 ms) | recording | +1 / −1 |
| Editing keys: backspace, arrows, Tab, Ctrl-C | recording | +2 |
| Irregular (median ≥ 0.5 s) / constant pauses between commands | recording | +1 / −2 |
| Commands without a terminal (`ssh host command`) | events | −2 |
| Client from an automation library (Go, libssh, paramiko, …) | events | −1 |

A score of 3 or more is `human`, −3 or less `bot`, `uncertain` in
between; a session with neither commands nor recording is
`no_interaction`. It is a heuristic: a human pasting commands sends whole
lines, a bot can fake typing. The thresholds, in `internal/verdict`, come
from a small sample and are meant to be tuned.

The verdict is also stored with each session, to filter the list:
`GET /api/sessions?verdict=human` or `verdict=bot` (bots include the
sessions without interaction; uncertain ones only show unfiltered). It
is computed at ingestion, again when the recording arrives, and when
the API starts for sessions judged by older criteria
(`verdict.Version`) or stored before verdicts were. The session page
computes it on read, with the same code.

## Configuration

Every option is a flag and an environment variable: `--db-dsn` is
`CAURI_DB_DSN`. A flag given on the command line wins.
`cauri <command> --help` lists them.

### `cauri api` and `cauri ui`

| Variable             | Default                                   | Meaning                                                                      |
|----------------------|-------------------------------------------|------------------------------------------------------------------------------|
| `CAURI_LISTEN`       | `:8080`                                   | Listen address (also `cauri ui`)                                             |
| `CAURI_DB_DRIVER`    | `sqlite`                                  | `sqlite` or `postgres`                                                       |
| `CAURI_DB_DSN`       | `data/cauri.db` (image: `/data/cauri.db`) | SQLite file path, or `postgres://user:password@host:5432/db?sslmode=disable` |
| `CAURI_TTY_DIR`      | `data/tty` (image: `/data/tty`)           | Directory of the TTY recordings                                              |
| `CAURI_GEOIP_DB` | (none) | MMDB GeoIP database (DB-IP Lite or GeoLite2) to locate sessions, see [GeoIP](#geoip) |
| `CAURI_MAX_TTY_SIZE` | `134217728` (128 MiB)                     | Largest recording accepted, in bytes                                         |
| `CAURI_UI`           | `false`                                   | `true`: same as `api --ui`                                                   |
| `CAURI_LOG_LEVEL`    | `info`                                    | `debug`, `info`, `warn`, `error` (also `cauri ui`)                           |

`cauri token` uses `CAURI_DB_DRIVER` and `CAURI_DB_DSN` too.

### `cauri ingestor`

| Variable               | Default                                   | Meaning                                                                                                   |
|------------------------|-------------------------------------------|-----------------------------------------------------------------------------------------------------------|
| `CAURI_API_URL`        | required                                  | Base URL of the API, such as `https://cauri.example.org`                                                  |
| `CAURI_TOKEN_FILE`     |                                           | File holding the token (preferred)                                                                        |
| `CAURI_TOKEN`          |                                           | The token itself                                                                                          |
| `CAURI_SENSOR_ID`      | required                                  | Identifier of the sensor: letters, digits, `.`, `_`, `-`; up to 64                                        |
| `CAURI_COWRIE_LOG_DIR` | required                                  | Cowrie's `var/log/cowrie`, holding `cowrie.json`                                                          |
| `CAURI_COWRIE_TTY_DIR` | required                                  | Cowrie's `var/lib/cowrie/tty`                                                                             |
| `CAURI_STATE_DIR`      | `data/ingestor` (image: `/data/ingestor`) | Writable directory: read position, pending uploads                                                        |
| `CAURI_START`          | `beginning`                               | Without saved position: `beginning` sends the existing logs, rotated ones included; `end` only new events |
| `CAURI_BATCH_SIZE`     | `500`                                     | Events per batch at most                                                                                  |
| `CAURI_FLUSH_INTERVAL` | `1s`                                      | Delay before sending an incomplete batch                                                                  |
| `CAURI_POLL_INTERVAL`  | `500ms`                                   | Delay between checks for new lines                                                                        |
| `CAURI_HTTP_TIMEOUT`   | `1m`                                      | Timeout of a request to the API                                                                           |
| `CAURI_LOG_LEVEL`      | `info`                                    | Log level                                                                                                 |

### Tokens

Each ingestor has its own token, bound to its sensor id: an ingestor
cannot send data in the name of another sensor. Only a hash of the token
is stored.

```sh
cauri token create --sensor hp-01 --name "vps-paris"   # prints the token once
cauri token list
cauri token revoke <id>                                # effective immediately
cauri token verify --token-file <file>                 # is this token valid? (exit code 1 if not)
```

With Docker: `docker compose exec api cauri token create --sensor hp-01`.

## Deployment

The image is `scratch` plus what the static binary needs, taken from
Alpine: CA certificates (the ingestor calls the API over HTTPS), time zones
(set `TZ` to log in local time; UTC otherwise) and a `nonroot` user. It
runs as that user (uid 65532). Its `/data`
directory is owned by that user, so a new named volume mounted there is
writable. With a bind mount instead, give the directory to uid 65532.

### Reverse proxy

cauri expects a reverse proxy in front of it, whichever you use (Traefik,
Caddy, nginx, HAProxy…). It terminates TLS and serves cauri under one
domain:

- mode 1: `/api` and everything below it to the `api` container, every
  other path to the `ui` container;
- mode 2: the whole domain to the `api` container (`api --ui`);
- ingestors on other hosts must reach `/api/ingest`;
- TTY uploads can weigh several megabytes (up to `CAURI_MAX_TTY_SIZE`):
  raise the body size limit of proxies that have a small one, such as
  nginx (`client_max_body_size`);
- containers listen on port 8080.

The compose files in `deploy/` are examples for Traefik, configured by
Docker labels. With another proxy, drop the `labels` and the `traefik`
network, then attach the containers to your proxy's network or publish
their port.

### Compose examples

Copy `deploy/.env.example` to `deploy/.env` and fill it in: image, domain,
Traefik network, entrypoint and certificate resolver, sensor id, Cowrie
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

The proxy sends `/api` to `api` and the rest of the domain to `ui`: one
origin, no CORS, no API URL in the UI.

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
sensor. TLS is handled by the reverse proxy in front of the API.

### PostgreSQL

SQLite is the default and the recommended choice: the compose files use
it. PostgreSQL is supported as an option, on a server you run: set
`CAURI_DB_DRIVER=postgres` and `CAURI_DB_DSN` to its URL, for example
`postgres://cauri:<password>@<host>:5432/cauri?sslmode=require`. Back it
up with the TTY directory, like the SQLite file.

### Protecting the UI

**The UI and the read API have no authentication yet.** Until they do,
do not expose them as is: protect them at the reverse proxy (basic
authentication, single sign-on, IP allow list) or keep them on a private
network or behind a VPN. Leave `/api/ingest` out of that protection:
ingestors authenticate with their tokens.

With the Traefik examples, ingestion has its own router (`cauri-ingest`),
so a middleware added to the other routers (`cauri-api` and `cauri-ui` in
mode 1, `cauri` in mode 2) does not block ingestors:

```yaml
traefik.http.routers.cauri-ui.middlewares: <your middleware>@<provider>
traefik.http.routers.cauri-api.middlewares: <your middleware>@<provider>
```

In the code, read routes go through a `ReadAuth` middleware (`internal/api`), a no-op today, where authentication will
plug in.

### Backups

Back up the database (the `cauri.db` file of the `cauri-data` volume, or
PostgreSQL) and the TTY directory together. The ingestor state can be
lost: the ingestor then sends everything again and the API drops what it
has.

## Development

Setting up a development environment, running the whole chain locally
with a real Cowrie, and checking a change: see
[docs/development.md](docs/development.md). In short:

```sh
make ui       # npm ci && npm run build in ui/, output in internal/webui/dist
make build    # bin/cauri, static, UI embedded if built
make test
make lint

# Optional, when changing SQL: run the tests on PostgreSQL too
docker run -d --rm -p 127.0.0.1:55432:5432 -e POSTGRES_PASSWORD=test postgres:17-alpine
CAURI_TEST_PG_DSN='postgres://postgres:test@127.0.0.1:55432/postgres?sslmode=disable' make test

# UI with hot reload, against a local "cauri api" (proxy for /api):
cd ui && npm run dev
```

UI texts are in `ui/src/locales/` (vue-i18n). English is the reference
catalogue; another language is a file with the same keys, checked by
TypeScript, registered in `ui/src/i18n.ts`.

`internal/ingest/testdata` holds a `cowrie.json` written by the official
`cowrie/cowrie` image (v3.1.2); tests check the parsing against it.

### CI and releases

Two equivalent workflows run the checks above (gofmt, vet, race tests on
SQLite, golangci-lint, UI lint and build) on every push and pull request,
then publish:

| | GitHub (`.github/workflows/ci.yml`) | Forgejo (`.forgejo/workflows/ci.yml`) |
|---|---|---|
| Image, on `main` and `v*` tags | `ghcr.io/devleesch001/cauri` | Registry of the instance |
| Release, on `v*` tags | GitHub release by GoReleaser | Forgejo release by GoReleaser |
| Secrets | None: the built-in `GITHUB_TOKEN` | `REGISTRY_TOKEN` (writes packages), optional `REGISTRY_USER`; `RELEASE_TOKEN` for the release if the built-in token cannot create it |

Both use the same `.goreleaser.yaml`: GoReleaser publishes to the forge
whose token is set (`GITHUB_TOKEN` or `GITEA_TOKEN`), and on Forgejo
finds the instance through `GITHUB_SERVER_URL`, set by the runner. Locally,
`make release` publishes to GitHub with `GITHUB_TOKEN`, and
`make release-forgejo` to the Forgejo instance `FORGEJO_URL` with
`GITEA_TOKEN`.

Image tags, the same on both forges (no `v`):

| Git event | Image tags |
|---|---|
| Stable tag `v1.2.3` | `1.2.3`, `1.2`, `1`, `latest` (no `0` while the major is 0: every `0.y` may break) |
| Pre-release tag `v1.2.3-rc.1`, `v1.2.3-beta.2`… | `1.2.3-rc.1` and `next` (the latest pre-release) |
| Push to `main` | `main`, `sha-<commit>` |

Images carry the standard OCI labels (source, version, revision,
creation date). GoReleaser publishes pre-release tags as pre-releases, so
they never become the latest release.

A release holds the static binaries (linux amd64 and arm64), deb, rpm and
Arch Linux packages installing `/usr/bin/cauri`, and checksums. To publish
one, push a tag:

```sh
git tag v0.1.0 && git push origin v0.1.0
```

Locally, `make release-snapshot` builds everything (multi-arch image
included, named after `IMAGE`, default `cauri`) in `dist/` without
publishing; `make release-build` builds the binaries only.

GoReleaser builds images from its own binaries with
`Dockerfile.goreleaser` (same layout as the `Dockerfile`); the CI image
jobs use the `Dockerfile`, which also builds the UI.

## Next steps the code is ready for

- **Statistics**: queries on the existing tables.
- **UI authentication**: a `ReadAuth` middleware.
- **All-in-one binary** (build tag `allinone`): the ingestor writes to a
  `Sink` interface; an in-process implementation calling
  `ingest.Service` replaces HTTPS.
- **GeoIP**, message broker: not designed in yet, nothing prevents them.

## License

MIT, see [LICENSE](LICENSE).

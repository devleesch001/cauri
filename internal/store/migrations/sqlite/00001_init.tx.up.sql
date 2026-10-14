-- Schema of cauri for SQLite. The postgres/ directory holds the
-- same tables, columns and indexes with the types of the other engine
-- (see docs/db.md). Times are microseconds since 1970-01-01 UTC,
-- durations milliseconds.

CREATE TABLE sensors (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE, -- the id the operator gives the ingestor
    first_seen INTEGER,              -- null until the first event
    last_seen  INTEGER
);

--bun:split

CREATE TABLE ingest_tokens (
    id           INTEGER PRIMARY KEY,
    sensor_id    INTEGER NOT NULL REFERENCES sensors (id),
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at   INTEGER
);

--bun:split

CREATE TABLE tty_files (
    id        INTEGER PRIMARY KEY,
    sha256    TEXT NOT NULL UNIQUE, -- of the file, names it in the TTY volume
    size      INTEGER NOT NULL,
    path      TEXT NOT NULL,
    stored_at INTEGER NOT NULL
);

--bun:split

CREATE TABLE sessions (
    id              INTEGER PRIMARY KEY,
    sensor_id       INTEGER NOT NULL REFERENCES sensors (id),
    cowrie_session  TEXT NOT NULL, -- Cowrie's id, unique within a sensor
    src_ip          TEXT,
    src_port        INTEGER,
    dst_ip          TEXT,
    dst_port        INTEGER,
    protocol        TEXT,
    client_version  TEXT,
    hassh           TEXT,
    started_at      INTEGER NOT NULL,
    ended_at        INTEGER,
    duration_ms     INTEGER,
    login_success   INTEGER NOT NULL DEFAULT 0,
    tty_log         TEXT, -- Cowrie's name for the recording, not its digest
    tty_duration_ms INTEGER,
    tty_file_id     INTEGER REFERENCES tty_files (id), -- set when uploaded
    geo_country     TEXT, -- ISO 3166-1 alpha-2
    geo_city        TEXT,
    geo_source      TEXT, -- GeoIP database used, set even if not found
    verdict         TEXT,
    verdict_score   INTEGER,
    verdict_version TEXT,
    UNIQUE (sensor_id, cowrie_session)
);

--bun:split

CREATE INDEX sessions_started_at ON sessions (started_at);

--bun:split

CREATE INDEX sessions_sensor_started_at ON sessions (sensor_id, started_at);

--bun:split

CREATE INDEX sessions_src_ip ON sessions (src_ip);

--bun:split

CREATE INDEX sessions_geo_country ON sessions (geo_country);

--bun:split

CREATE INDEX sessions_verdict ON sessions (verdict);

--bun:split

CREATE TABLE events (
    id          INTEGER PRIMARY KEY,
    hash        BLOB NOT NULL UNIQUE, -- SHA-256 of sensor and line: deduplication
    sensor_id   INTEGER NOT NULL REFERENCES sensors (id),
    session_id  INTEGER REFERENCES sessions (id),
    type        TEXT NOT NULL, -- Cowrie's eventid
    ts          INTEGER NOT NULL,
    received_at INTEGER NOT NULL
);

--bun:split

CREATE INDEX events_session_ts ON events (session_id, ts);

--bun:split

CREATE TABLE event_data (
    event_id INTEGER PRIMARY KEY REFERENCES events (id) ON DELETE CASCADE,
    data     TEXT NOT NULL -- the Cowrie line; can be purged
);

--bun:split

CREATE TABLE login_attempts (
    id         INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions (id),
    ts         INTEGER NOT NULL,
    username   TEXT NOT NULL,
    password   TEXT NOT NULL,
    success    INTEGER NOT NULL
);

--bun:split

CREATE INDEX login_attempts_session ON login_attempts (session_id);

--bun:split

CREATE INDEX login_attempts_ts ON login_attempts (ts);

--bun:split

CREATE INDEX login_attempts_username ON login_attempts (username, session_id);

--bun:split

CREATE TABLE commands (
    id         INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions (id),
    ts         INTEGER NOT NULL,
    input      TEXT NOT NULL
);

--bun:split

CREATE INDEX commands_session ON commands (session_id);

--bun:split

CREATE TABLE transfers (
    id         INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions (id),
    ts         INTEGER NOT NULL,
    direction  TEXT NOT NULL, -- download or upload
    url        TEXT,
    filename   TEXT,
    sha256     TEXT
);

--bun:split

CREATE INDEX transfers_session ON transfers (session_id);

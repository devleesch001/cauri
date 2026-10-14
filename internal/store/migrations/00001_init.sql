-- Portable between SQLite and PostgreSQL: no auto-increment, TIMESTAMPTZ
-- (NUMERIC affinity in SQLite, always written in UTC by the application),
-- JSON kept as TEXT. Identifiers are computed by the application.

-- +goose Up
CREATE TABLE sensors (
    id         TEXT PRIMARY KEY,
    first_seen TIMESTAMPTZ NOT NULL,
    last_seen  TIMESTAMPTZ NOT NULL
);

CREATE TABLE ingest_tokens (
    id           TEXT PRIMARY KEY,
    sensor_id    TEXT NOT NULL,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   TIMESTAMPTZ NOT NULL,
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ
);

CREATE TABLE tty_files (
    sha256    TEXT PRIMARY KEY,
    size      BIGINT NOT NULL,
    path      TEXT NOT NULL,
    stored_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE sessions (
    id             TEXT PRIMARY KEY,
    sensor_id      TEXT NOT NULL REFERENCES sensors (id),
    session        TEXT NOT NULL,
    src_ip         TEXT,
    src_port       INTEGER,
    dst_ip         TEXT,
    dst_port       INTEGER,
    protocol       TEXT,
    client_version TEXT,
    hassh          TEXT,
    started_at     TIMESTAMPTZ NOT NULL,
    ended_at       TIMESTAMPTZ,
    duration       DOUBLE PRECISION,
    login_success  BOOLEAN NOT NULL DEFAULT FALSE,
    -- From cowrie.log.closed: Cowrie's name for the recording, which is
    -- not the digest of the file, and the recording duration.
    tty_log        TEXT,
    tty_duration   DOUBLE PRECISION,
    -- Set when the recording is uploaded: digest and size of the file.
    tty_sha256     TEXT,
    tty_size       BIGINT,
    UNIQUE (sensor_id, session)
);
CREATE INDEX sessions_started_at ON sessions (started_at);
CREATE INDEX sessions_src_ip ON sessions (src_ip);
CREATE INDEX sessions_tty_sha256 ON sessions (tty_sha256);

CREATE TABLE raw_events (
    id          TEXT PRIMARY KEY,
    sensor_id   TEXT NOT NULL REFERENCES sensors (id),
    session_id  TEXT REFERENCES sessions (id),
    eventid     TEXT NOT NULL,
    ts          TIMESTAMPTZ NOT NULL,
    src_ip      TEXT,
    data        TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX raw_events_session_ts ON raw_events (session_id, ts);
CREATE INDEX raw_events_eventid_ts ON raw_events (eventid, ts);

CREATE TABLE login_attempts (
    id         TEXT PRIMARY KEY REFERENCES raw_events (id),
    session_id TEXT NOT NULL REFERENCES sessions (id),
    ts         TIMESTAMPTZ NOT NULL,
    username   TEXT NOT NULL,
    password   TEXT NOT NULL,
    success    BOOLEAN NOT NULL
);
CREATE INDEX login_attempts_session ON login_attempts (session_id);
CREATE INDEX login_attempts_ts ON login_attempts (ts);
CREATE INDEX login_attempts_username ON login_attempts (username);

CREATE TABLE commands (
    id         TEXT PRIMARY KEY REFERENCES raw_events (id),
    session_id TEXT NOT NULL REFERENCES sessions (id),
    ts         TIMESTAMPTZ NOT NULL,
    input      TEXT NOT NULL
);
CREATE INDEX commands_session ON commands (session_id);

CREATE TABLE transfers (
    id         TEXT PRIMARY KEY REFERENCES raw_events (id),
    session_id TEXT NOT NULL REFERENCES sessions (id),
    ts         TIMESTAMPTZ NOT NULL,
    direction  TEXT NOT NULL,
    url        TEXT,
    filename   TEXT,
    sha256     TEXT
);
CREATE INDEX transfers_session ON transfers (session_id);
CREATE INDEX transfers_sha256 ON transfers (sha256);

-- +goose Down
DROP TABLE transfers;
DROP TABLE commands;
DROP TABLE login_attempts;
DROP TABLE raw_events;
DROP TABLE sessions;
DROP TABLE tty_files;
DROP TABLE ingest_tokens;
DROP TABLE sensors;

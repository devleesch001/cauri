-- Back to unnamed foreign keys without actions, and without the indexes of
-- the foreign keys: the same rebuild as the migration, the other way round.

CREATE TABLE ingest_tokens_n (
    id           INTEGER PRIMARY KEY,
    sensor_id    INTEGER NOT NULL REFERENCES sensors (id),
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at   INTEGER
);

--bun:split

CREATE TABLE sessions_n (
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
    geo_lat         REAL,
    geo_lon         REAL,
    asn             INTEGER,
    as_org          TEXT,
    UNIQUE (sensor_id, cowrie_session)
);

--bun:split

CREATE TABLE events_n (
    id          INTEGER PRIMARY KEY,
    hash        BLOB NOT NULL UNIQUE, -- SHA-256 of sensor and line: deduplication
    sensor_id   INTEGER NOT NULL REFERENCES sensors (id),
    session_id  INTEGER REFERENCES sessions_n (id),
    type        TEXT NOT NULL, -- Cowrie's eventid
    ts          INTEGER NOT NULL,
    received_at INTEGER NOT NULL
);

--bun:split

CREATE TABLE event_data_n (
    event_id INTEGER PRIMARY KEY REFERENCES events_n (id) ON DELETE CASCADE,
    data     TEXT NOT NULL -- the Cowrie line; can be purged
);

--bun:split

CREATE TABLE login_attempts_n (
    id         INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions_n (id),
    ts         INTEGER NOT NULL,
    username   TEXT NOT NULL,
    password   TEXT NOT NULL,
    success    INTEGER NOT NULL
);

--bun:split

CREATE TABLE commands_n (
    id         INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions_n (id),
    ts         INTEGER NOT NULL,
    input      TEXT NOT NULL
);

--bun:split

CREATE TABLE transfers_n (
    id         INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL REFERENCES sessions_n (id),
    ts         INTEGER NOT NULL,
    direction  TEXT NOT NULL, -- download or upload
    url        TEXT,
    filename   TEXT,
    sha256     TEXT
);

--bun:split

INSERT INTO ingest_tokens_n (id, sensor_id, name, token_hash, created_at, last_used_at, revoked_at)
SELECT id, sensor_id, name, token_hash, created_at, last_used_at, revoked_at FROM ingest_tokens;

--bun:split

INSERT INTO sessions_n (id, sensor_id, cowrie_session, src_ip, src_port, dst_ip, dst_port, protocol, client_version,
    hassh, started_at, ended_at, duration_ms, login_success, tty_log, tty_duration_ms, tty_file_id, geo_country,
    geo_city, geo_source, verdict, verdict_score, verdict_version, geo_lat, geo_lon, asn, as_org)
SELECT id, sensor_id, cowrie_session, src_ip, src_port, dst_ip, dst_port, protocol, client_version,
    hassh, started_at, ended_at, duration_ms, login_success, tty_log, tty_duration_ms, tty_file_id, geo_country,
    geo_city, geo_source, verdict, verdict_score, verdict_version, geo_lat, geo_lon, asn, as_org FROM sessions;

--bun:split

INSERT INTO events_n (id, hash, sensor_id, session_id, type, ts, received_at)
SELECT id, hash, sensor_id, session_id, type, ts, received_at FROM events;

--bun:split

INSERT INTO event_data_n (event_id, data) SELECT event_id, data FROM event_data;

--bun:split

INSERT INTO login_attempts_n (id, session_id, ts, username, password, success)
SELECT id, session_id, ts, username, password, success FROM login_attempts;

--bun:split

INSERT INTO commands_n (id, session_id, ts, input) SELECT id, session_id, ts, input FROM commands;

--bun:split

INSERT INTO transfers_n (id, session_id, ts, direction, url, filename, sha256)
SELECT id, session_id, ts, direction, url, filename, sha256 FROM transfers;

--bun:split

DROP TABLE event_data;

--bun:split

DROP TABLE login_attempts;

--bun:split

DROP TABLE commands;

--bun:split

DROP TABLE transfers;

--bun:split

DROP TABLE events;

--bun:split

DROP TABLE sessions;

--bun:split

DROP TABLE ingest_tokens;

--bun:split

ALTER TABLE ingest_tokens_n RENAME TO ingest_tokens;

--bun:split

ALTER TABLE sessions_n RENAME TO sessions;

--bun:split

ALTER TABLE events_n RENAME TO events;

--bun:split

ALTER TABLE event_data_n RENAME TO event_data;

--bun:split

ALTER TABLE login_attempts_n RENAME TO login_attempts;

--bun:split

ALTER TABLE commands_n RENAME TO commands;

--bun:split

ALTER TABLE transfers_n RENAME TO transfers;

--bun:split

CREATE INDEX sessions_started_at ON sessions (started_at);

--bun:split

CREATE INDEX sessions_sensor_started_at ON sessions (sensor_id, started_at);

--bun:split

CREATE INDEX sessions_src_ip ON sessions (src_ip);

--bun:split

CREATE INDEX sessions_verdict ON sessions (verdict);

--bun:split

CREATE INDEX sessions_geo_place ON sessions (geo_country, geo_city);

--bun:split

CREATE INDEX sessions_asn ON sessions (asn);

--bun:split

CREATE INDEX events_session_ts ON events (session_id, ts);

--bun:split

CREATE INDEX login_attempts_session ON login_attempts (session_id);

--bun:split

CREATE INDEX login_attempts_ts ON login_attempts (ts);

--bun:split

CREATE INDEX login_attempts_username ON login_attempts (username, session_id);

--bun:split

CREATE INDEX commands_session ON commands (session_id);

--bun:split

CREATE INDEX transfers_session ON transfers (session_id);

--bun:split

CREATE INDEX transfers_sha256 ON transfers (sha256);

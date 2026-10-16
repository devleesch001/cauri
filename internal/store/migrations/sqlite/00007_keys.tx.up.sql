-- Every key and index named, the same in both engines (AGENTS.md): pk_,
-- uk_, fk_ and ix_ instead of the names SQLite chose; every foreign key
-- also follows its parent's id (ON UPDATE CASCADE). SQLite renames
-- neither a constraint nor an index, so every table is rebuilt, as in
-- 00005: each new table is created under a temporary name, referring to the
-- others' temporary names, and filled; the old tables are dropped, children
-- first, and the new ones renamed, which makes SQLite rewrite the references
-- to their final names. Primary keys become named table constraints (an
-- INTEGER key stays the rowid); unique keys become named unique indexes,
-- since SQLite gives inline ones automatic names. Rows and ids are kept.

CREATE TABLE sensors_n (
    id         INTEGER NOT NULL,
    name       TEXT NOT NULL, -- the id the operator gives the ingestor
    first_seen INTEGER, -- null until the first event
    last_seen  INTEGER,
    CONSTRAINT pk_sensors PRIMARY KEY (id)
);

--bun:split

CREATE TABLE tty_files_n (
    id        INTEGER NOT NULL,
    sha256    TEXT NOT NULL, -- of the file, names it in the TTY volume
    size      INTEGER NOT NULL,
    path      TEXT NOT NULL,
    stored_at INTEGER NOT NULL,
    CONSTRAINT pk_tty_files PRIMARY KEY (id)
);

--bun:split

CREATE TABLE ingest_tokens_n (
    id           INTEGER NOT NULL,
    sensor_id    INTEGER NOT NULL,
    name         TEXT NOT NULL,
    token_hash   TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER,
    revoked_at   INTEGER,
    CONSTRAINT pk_ingest_tokens PRIMARY KEY (id),
    CONSTRAINT fk_ingest_tokens_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors_n (id) ON DELETE CASCADE ON UPDATE CASCADE
);

--bun:split

CREATE TABLE sessions_n (
    id              INTEGER NOT NULL,
    sensor_id       INTEGER NOT NULL,
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
    tty_file_id     INTEGER, -- set when uploaded
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
    shell           INTEGER,
    CONSTRAINT pk_sessions PRIMARY KEY (id),
    CONSTRAINT fk_sessions_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors_n (id) ON DELETE RESTRICT ON UPDATE CASCADE,
    CONSTRAINT fk_sessions_tty_file_id FOREIGN KEY (tty_file_id) REFERENCES tty_files_n (id) ON DELETE SET NULL ON UPDATE CASCADE
);

--bun:split

CREATE TABLE events_n (
    id          INTEGER NOT NULL,
    hash        BLOB NOT NULL, -- SHA-256 of sensor and line: deduplication
    sensor_id   INTEGER NOT NULL,
    session_id  INTEGER,
    type        TEXT NOT NULL, -- Cowrie's eventid
    ts          INTEGER NOT NULL,
    received_at INTEGER NOT NULL,
    CONSTRAINT pk_events PRIMARY KEY (id),
    CONSTRAINT fk_events_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors_n (id) ON DELETE RESTRICT ON UPDATE CASCADE,
    CONSTRAINT fk_events_session_id FOREIGN KEY (session_id) REFERENCES sessions_n (id) ON DELETE CASCADE ON UPDATE CASCADE
);

--bun:split

CREATE TABLE event_data_n (
    event_id INTEGER NOT NULL,
    data     TEXT NOT NULL, -- the Cowrie line; can be purged
    CONSTRAINT pk_event_data PRIMARY KEY (event_id),
    CONSTRAINT fk_event_data_event_id FOREIGN KEY (event_id) REFERENCES events_n (id) ON DELETE CASCADE ON UPDATE CASCADE
);

--bun:split

CREATE TABLE login_attempts_n (
    id         INTEGER NOT NULL,
    session_id INTEGER NOT NULL,
    ts         INTEGER NOT NULL,
    username   TEXT NOT NULL,
    password   TEXT NOT NULL,
    success    INTEGER NOT NULL,
    CONSTRAINT pk_login_attempts PRIMARY KEY (id),
    CONSTRAINT fk_login_attempts_session_id FOREIGN KEY (session_id) REFERENCES sessions_n (id) ON DELETE CASCADE ON UPDATE CASCADE
);

--bun:split

CREATE TABLE commands_n (
    id         INTEGER NOT NULL,
    session_id INTEGER NOT NULL,
    ts         INTEGER NOT NULL,
    input      TEXT NOT NULL,
    CONSTRAINT pk_commands PRIMARY KEY (id),
    CONSTRAINT fk_commands_session_id FOREIGN KEY (session_id) REFERENCES sessions_n (id) ON DELETE CASCADE ON UPDATE CASCADE
);

--bun:split

CREATE TABLE transfers_n (
    id         INTEGER NOT NULL,
    session_id INTEGER NOT NULL,
    ts         INTEGER NOT NULL,
    direction  TEXT NOT NULL, -- download or upload
    url        TEXT,
    filename   TEXT,
    sha256     TEXT,
    CONSTRAINT pk_transfers PRIMARY KEY (id),
    CONSTRAINT fk_transfers_session_id FOREIGN KEY (session_id) REFERENCES sessions_n (id) ON DELETE CASCADE ON UPDATE CASCADE
);

--bun:split

INSERT INTO sensors_n (id, name, first_seen, last_seen)
SELECT id, name, first_seen, last_seen FROM sensors;

--bun:split

INSERT INTO tty_files_n (id, sha256, size, path, stored_at)
SELECT id, sha256, size, path, stored_at FROM tty_files;

--bun:split

INSERT INTO ingest_tokens_n (id, sensor_id, name, token_hash, created_at, last_used_at, revoked_at)
SELECT id, sensor_id, name, token_hash, created_at, last_used_at, revoked_at FROM ingest_tokens;

--bun:split

INSERT INTO sessions_n (id, sensor_id, cowrie_session, src_ip, src_port, dst_ip, dst_port, protocol, client_version, hassh, started_at, ended_at, duration_ms, login_success, tty_log, tty_duration_ms, tty_file_id, geo_country, geo_city, geo_source, verdict, verdict_score, verdict_version, geo_lat, geo_lon, asn, as_org, shell)
SELECT id, sensor_id, cowrie_session, src_ip, src_port, dst_ip, dst_port, protocol, client_version, hassh, started_at, ended_at, duration_ms, login_success, tty_log, tty_duration_ms, tty_file_id, geo_country, geo_city, geo_source, verdict, verdict_score, verdict_version, geo_lat, geo_lon, asn, as_org, shell FROM sessions;

--bun:split

INSERT INTO events_n (id, hash, sensor_id, session_id, type, ts, received_at)
SELECT id, hash, sensor_id, session_id, type, ts, received_at FROM events;

--bun:split

INSERT INTO event_data_n (event_id, data)
SELECT event_id, data FROM event_data;

--bun:split

INSERT INTO login_attempts_n (id, session_id, ts, username, password, success)
SELECT id, session_id, ts, username, password, success FROM login_attempts;

--bun:split

INSERT INTO commands_n (id, session_id, ts, input)
SELECT id, session_id, ts, input FROM commands;

--bun:split

INSERT INTO transfers_n (id, session_id, ts, direction, url, filename, sha256)
SELECT id, session_id, ts, direction, url, filename, sha256 FROM transfers;

--bun:split

DROP TABLE transfers;

--bun:split

DROP TABLE commands;

--bun:split

DROP TABLE login_attempts;

--bun:split

DROP TABLE event_data;

--bun:split

DROP TABLE events;

--bun:split

DROP TABLE sessions;

--bun:split

DROP TABLE ingest_tokens;

--bun:split

DROP TABLE tty_files;

--bun:split

DROP TABLE sensors;

--bun:split

ALTER TABLE sensors_n RENAME TO sensors;

--bun:split

ALTER TABLE tty_files_n RENAME TO tty_files;

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

CREATE UNIQUE INDEX uk_sensors_name ON sensors (name);

--bun:split

CREATE UNIQUE INDEX uk_tty_files_sha256 ON tty_files (sha256);

--bun:split

CREATE UNIQUE INDEX uk_ingest_tokens_token_hash ON ingest_tokens (token_hash);

--bun:split

CREATE UNIQUE INDEX uk_sessions_sensor_id_cowrie_session ON sessions (sensor_id, cowrie_session);

--bun:split

CREATE UNIQUE INDEX uk_events_hash ON events (hash);

--bun:split

CREATE INDEX ix_ingest_tokens_sensor_id ON ingest_tokens (sensor_id);

--bun:split

CREATE INDEX ix_sessions_started_at ON sessions (started_at);

--bun:split

CREATE INDEX ix_sessions_sensor_id_started_at ON sessions (sensor_id, started_at);

--bun:split

CREATE INDEX ix_sessions_src_ip ON sessions (src_ip);

--bun:split

CREATE INDEX ix_sessions_verdict ON sessions (verdict);

--bun:split

CREATE INDEX ix_sessions_geo_country_geo_city ON sessions (geo_country, geo_city);

--bun:split

CREATE INDEX ix_sessions_asn ON sessions (asn);

--bun:split

CREATE INDEX ix_sessions_tty_file_id ON sessions (tty_file_id);

--bun:split

CREATE INDEX ix_sessions_shell ON sessions (shell);

--bun:split

CREATE INDEX ix_events_session_id_ts ON events (session_id, ts);

--bun:split

CREATE INDEX ix_events_sensor_id ON events (sensor_id);

--bun:split

CREATE INDEX ix_login_attempts_session_id ON login_attempts (session_id);

--bun:split

CREATE INDEX ix_login_attempts_ts ON login_attempts (ts);

--bun:split

CREATE INDEX ix_login_attempts_username_session_id ON login_attempts (username, session_id);

--bun:split

CREATE INDEX ix_commands_session_id ON commands (session_id);

--bun:split

CREATE INDEX ix_transfers_session_id ON transfers (session_id);

--bun:split

CREATE INDEX ix_transfers_sha256 ON transfers (sha256);

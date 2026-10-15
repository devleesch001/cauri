-- A session has a recording per SSH channel: many bots run each command on
-- its own exec channel. Cowrie names a recording by the SHA-256 of its
-- input (the keys typed, or the command of an exec channel) and keeps one
-- file per name: a later recording of the same input is marked duplicate,
-- and its file, with its times, is the first one's, maybe of another
-- session. started_at places each recording in its own session.

CREATE TABLE session_recordings (
    id          INTEGER PRIMARY KEY,
    session_id  INTEGER NOT NULL REFERENCES sessions (id),
    ttylog      TEXT NOT NULL,    -- Cowrie's name: SHA-256 of the input
    started_at  INTEGER NOT NULL, -- the channel's start: log closed - duration
    duration_ms INTEGER,
    duplicate   INTEGER NOT NULL DEFAULT 0,
    tty_file_id INTEGER REFERENCES tty_files (id) -- set when uploaded
);

--bun:split

CREATE INDEX session_recordings_session ON session_recordings (session_id, started_at);

--bun:split

-- The recordings told by the cowrie.log.closed lines still stored. Fields
-- of an unexpected type are left out rather than failing the migration.
INSERT INTO session_recordings (session_id, ttylog, started_at, duration_ms, duplicate)
SELECT session_id, replace(p, rtrim(p, replace(p, '/', '')), ''), ts - COALESCE(dur, 0) * 1000, dur, dup
FROM (
    SELECT e.session_id, e.ts,
        CASE WHEN json_type(d.data, '$.ttylog') = 'text' THEN json_extract(d.data, '$.ttylog') END AS p,
        CASE
            WHEN json_type(d.data, '$.duration_ms') IN ('integer', 'real')
                AND json_extract(d.data, '$.duration_ms') BETWEEN 0 AND 1e15
                THEN CAST(round(json_extract(d.data, '$.duration_ms')) AS INTEGER)
            WHEN json_type(d.data, '$.duration') IN ('integer', 'real')
                AND json_extract(d.data, '$.duration') BETWEEN 0 AND 1e12
                THEN CAST(round(json_extract(d.data, '$.duration') * 1000) AS INTEGER)
        END AS dur,
        CASE WHEN json_type(d.data, '$.duplicate') = 'true' THEN 1 ELSE 0 END AS dup
    FROM events AS e
    JOIN event_data AS d ON d.event_id = e.id
    WHERE e.type = 'cowrie.log.closed' AND e.session_id IS NOT NULL AND json_valid(d.data)
)
WHERE p IS NOT NULL AND replace(p, rtrim(p, replace(p, '/', '')), '') <> '';

--bun:split

-- Sessions whose lines were purged keep the one recording they knew.
INSERT INTO session_recordings (session_id, ttylog, started_at, duration_ms, duplicate)
SELECT s.id, COALESCE(s.tty_log, ''),
    CASE
        WHEN COALESCE(s.ended_at, s.started_at) - COALESCE(s.tty_duration_ms, 0) * 1000 < s.started_at THEN s.started_at
        ELSE COALESCE(s.ended_at, s.started_at) - COALESCE(s.tty_duration_ms, 0) * 1000
    END,
    s.tty_duration_ms, 0
FROM sessions AS s
WHERE (s.tty_log IS NOT NULL OR s.tty_file_id IS NOT NULL)
    AND NOT EXISTS (SELECT 1 FROM session_recordings AS r WHERE r.session_id = s.id);

--bun:split

-- A session could link one file: the ingestor uploaded the recording of
-- its first channel, and the API refused the others. It goes to the first
-- recording, and to the later ones of the same input (same Cowrie file).
UPDATE session_recordings
SET tty_file_id = (SELECT s.tty_file_id FROM sessions AS s WHERE s.id = session_recordings.session_id)
WHERE session_id IN (SELECT id FROM sessions WHERE tty_file_id IS NOT NULL)
    AND ttylog = (
        SELECT f.ttylog FROM session_recordings AS f
        WHERE f.session_id = session_recordings.session_id
        ORDER BY f.started_at, f.id LIMIT 1
    );

--bun:split

-- SQLite cannot drop a column holding a foreign key: the table is rebuilt.
-- The rows of other tables are orphans from the drop to the copy back,
-- which deferred checks allow until the commit (their session_id columns
-- are indexed: the drop checks them).
PRAGMA defer_foreign_keys = ON;

--bun:split

CREATE TABLE sessions_copy AS SELECT * FROM sessions;

--bun:split

DROP TABLE sessions;

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

INSERT INTO sessions (id, sensor_id, cowrie_session, src_ip, src_port, dst_ip, dst_port, protocol,
    client_version, hassh, started_at, ended_at, duration_ms, login_success, geo_country, geo_city,
    geo_source, verdict, verdict_score, verdict_version, geo_lat, geo_lon, asn, as_org)
SELECT id, sensor_id, cowrie_session, src_ip, src_port, dst_ip, dst_port, protocol,
    client_version, hassh, started_at, ended_at, duration_ms, login_success, geo_country, geo_city,
    geo_source, verdict, verdict_score, verdict_version, geo_lat, geo_lon, asn, as_org
FROM sessions_copy;

--bun:split

DROP TABLE sessions_copy;

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

-- Back to one recording per session: the first one with a file, else the
-- first one.

ALTER TABLE sessions ADD COLUMN tty_log TEXT;

--bun:split

ALTER TABLE sessions ADD COLUMN tty_duration_ms INTEGER;

--bun:split

ALTER TABLE sessions ADD COLUMN tty_file_id INTEGER REFERENCES tty_files (id);

--bun:split

UPDATE sessions
SET (tty_log, tty_duration_ms, tty_file_id) = (
    SELECT NULLIF(r.ttylog, ''), r.duration_ms, r.tty_file_id FROM session_recordings AS r
    WHERE r.session_id = sessions.id
    ORDER BY r.tty_file_id IS NULL, r.started_at, r.id LIMIT 1
)
WHERE id IN (SELECT session_id FROM session_recordings);

--bun:split

DROP TABLE session_recordings;

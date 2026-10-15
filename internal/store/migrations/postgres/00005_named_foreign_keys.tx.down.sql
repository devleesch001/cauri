-- Back to unnamed foreign keys without actions (but the first migration's
-- cascade of event_data), and without the indexes of the foreign keys.

DROP INDEX events_sensor;

--bun:split

DROP INDEX sessions_tty_file;

--bun:split

DROP INDEX ingest_tokens_sensor;

--bun:split

ALTER TABLE transfers DROP CONSTRAINT fk_transfers_session_id;

--bun:split

ALTER TABLE transfers ADD FOREIGN KEY (session_id) REFERENCES sessions (id);

--bun:split

ALTER TABLE commands DROP CONSTRAINT fk_commands_session_id;

--bun:split

ALTER TABLE commands ADD FOREIGN KEY (session_id) REFERENCES sessions (id);

--bun:split

ALTER TABLE login_attempts DROP CONSTRAINT fk_login_attempts_session_id;

--bun:split

ALTER TABLE login_attempts ADD FOREIGN KEY (session_id) REFERENCES sessions (id);

--bun:split

ALTER TABLE event_data DROP CONSTRAINT fk_event_data_event_id;

--bun:split

ALTER TABLE event_data ADD FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE events DROP CONSTRAINT fk_events_session_id;

--bun:split

ALTER TABLE events ADD FOREIGN KEY (session_id) REFERENCES sessions (id);

--bun:split

ALTER TABLE events DROP CONSTRAINT fk_events_sensor_id;

--bun:split

ALTER TABLE events ADD FOREIGN KEY (sensor_id) REFERENCES sensors (id);

--bun:split

ALTER TABLE sessions DROP CONSTRAINT fk_sessions_tty_file_id;

--bun:split

ALTER TABLE sessions ADD FOREIGN KEY (tty_file_id) REFERENCES tty_files (id);

--bun:split

ALTER TABLE sessions DROP CONSTRAINT fk_sessions_sensor_id;

--bun:split

ALTER TABLE sessions ADD FOREIGN KEY (sensor_id) REFERENCES sensors (id);

--bun:split

ALTER TABLE ingest_tokens DROP CONSTRAINT fk_ingest_tokens_sensor_id;

--bun:split

ALTER TABLE ingest_tokens ADD FOREIGN KEY (sensor_id) REFERENCES sensors (id);

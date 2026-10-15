-- Named foreign keys, with the action each one needs when its parent goes:
-- see docs/db.md. PostgreSQL names the ones of the first migration
-- <table>_<column>_fkey.

ALTER TABLE ingest_tokens DROP CONSTRAINT ingest_tokens_sensor_id_fkey;

--bun:split

ALTER TABLE ingest_tokens ADD CONSTRAINT fk_ingest_tokens_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE sessions DROP CONSTRAINT sessions_sensor_id_fkey;

--bun:split

ALTER TABLE sessions ADD CONSTRAINT fk_sessions_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors (id) ON DELETE RESTRICT;

--bun:split

ALTER TABLE sessions DROP CONSTRAINT sessions_tty_file_id_fkey;

--bun:split

ALTER TABLE sessions ADD CONSTRAINT fk_sessions_tty_file_id FOREIGN KEY (tty_file_id) REFERENCES tty_files (id) ON DELETE SET NULL;

--bun:split

ALTER TABLE events DROP CONSTRAINT events_sensor_id_fkey;

--bun:split

ALTER TABLE events ADD CONSTRAINT fk_events_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors (id) ON DELETE RESTRICT;

--bun:split

ALTER TABLE events DROP CONSTRAINT events_session_id_fkey;

--bun:split

ALTER TABLE events ADD CONSTRAINT fk_events_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE event_data DROP CONSTRAINT event_data_event_id_fkey;

--bun:split

ALTER TABLE event_data ADD CONSTRAINT fk_event_data_event_id FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE login_attempts DROP CONSTRAINT login_attempts_session_id_fkey;

--bun:split

ALTER TABLE login_attempts ADD CONSTRAINT fk_login_attempts_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE commands DROP CONSTRAINT commands_session_id_fkey;

--bun:split

ALTER TABLE commands ADD CONSTRAINT fk_commands_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE transfers DROP CONSTRAINT transfers_session_id_fkey;

--bun:split

ALTER TABLE transfers ADD CONSTRAINT fk_transfers_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE;

--bun:split

CREATE INDEX ingest_tokens_sensor ON ingest_tokens (sensor_id);

--bun:split

CREATE INDEX sessions_tty_file ON sessions (tty_file_id);

--bun:split

CREATE INDEX events_sensor ON events (sensor_id);

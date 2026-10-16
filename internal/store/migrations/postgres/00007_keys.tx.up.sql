-- Every key and index named, the same in both engines (AGENTS.md): pk_,
-- uk_, fk_ and ix_ instead of the names PostgreSQL chose; every foreign key
-- also follows its parent's id (ON UPDATE CASCADE). Renaming a primary or
-- unique constraint renames its index too. No data is rewritten; adding a
-- foreign key back checks the existing rows.

ALTER TABLE sensors RENAME CONSTRAINT sensors_pkey TO pk_sensors;

--bun:split

ALTER TABLE tty_files RENAME CONSTRAINT tty_files_pkey TO pk_tty_files;

--bun:split

ALTER TABLE ingest_tokens RENAME CONSTRAINT ingest_tokens_pkey TO pk_ingest_tokens;

--bun:split

ALTER TABLE sessions RENAME CONSTRAINT sessions_pkey TO pk_sessions;

--bun:split

ALTER TABLE events RENAME CONSTRAINT events_pkey TO pk_events;

--bun:split

ALTER TABLE event_data RENAME CONSTRAINT event_data_pkey TO pk_event_data;

--bun:split

ALTER TABLE login_attempts RENAME CONSTRAINT login_attempts_pkey TO pk_login_attempts;

--bun:split

ALTER TABLE commands RENAME CONSTRAINT commands_pkey TO pk_commands;

--bun:split

ALTER TABLE transfers RENAME CONSTRAINT transfers_pkey TO pk_transfers;

--bun:split

ALTER TABLE sensors RENAME CONSTRAINT sensors_name_key TO uk_sensors_name;

--bun:split

ALTER TABLE tty_files RENAME CONSTRAINT tty_files_sha256_key TO uk_tty_files_sha256;

--bun:split

ALTER TABLE ingest_tokens RENAME CONSTRAINT ingest_tokens_token_hash_key TO uk_ingest_tokens_token_hash;

--bun:split

ALTER TABLE sessions RENAME CONSTRAINT sessions_sensor_id_cowrie_session_key TO uk_sessions_sensor_id_cowrie_session;

--bun:split

ALTER TABLE events RENAME CONSTRAINT events_hash_key TO uk_events_hash;

--bun:split

ALTER INDEX ingest_tokens_sensor RENAME TO ix_ingest_tokens_sensor_id;

--bun:split

ALTER INDEX sessions_started_at RENAME TO ix_sessions_started_at;

--bun:split

ALTER INDEX sessions_sensor_started_at RENAME TO ix_sessions_sensor_id_started_at;

--bun:split

ALTER INDEX sessions_src_ip RENAME TO ix_sessions_src_ip;

--bun:split

ALTER INDEX sessions_verdict RENAME TO ix_sessions_verdict;

--bun:split

ALTER INDEX sessions_geo_place RENAME TO ix_sessions_geo_country_geo_city;

--bun:split

ALTER INDEX sessions_asn RENAME TO ix_sessions_asn;

--bun:split

ALTER INDEX sessions_tty_file RENAME TO ix_sessions_tty_file_id;

--bun:split

ALTER INDEX sessions_shell RENAME TO ix_sessions_shell;

--bun:split

ALTER INDEX events_session_ts RENAME TO ix_events_session_id_ts;

--bun:split

ALTER INDEX events_sensor RENAME TO ix_events_sensor_id;

--bun:split

ALTER INDEX login_attempts_session RENAME TO ix_login_attempts_session_id;

--bun:split

ALTER INDEX login_attempts_ts RENAME TO ix_login_attempts_ts;

--bun:split

ALTER INDEX login_attempts_username RENAME TO ix_login_attempts_username_session_id;

--bun:split

ALTER INDEX commands_session RENAME TO ix_commands_session_id;

--bun:split

ALTER INDEX transfers_session RENAME TO ix_transfers_session_id;

--bun:split

ALTER INDEX transfers_sha256 RENAME TO ix_transfers_sha256;

--bun:split

ALTER TABLE ingest_tokens DROP CONSTRAINT fk_ingest_tokens_sensor_id;

--bun:split

ALTER TABLE ingest_tokens ADD CONSTRAINT fk_ingest_tokens_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors (id) ON DELETE CASCADE ON UPDATE CASCADE;

--bun:split

ALTER TABLE sessions DROP CONSTRAINT fk_sessions_sensor_id;

--bun:split

ALTER TABLE sessions ADD CONSTRAINT fk_sessions_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors (id) ON DELETE RESTRICT ON UPDATE CASCADE;

--bun:split

ALTER TABLE sessions DROP CONSTRAINT fk_sessions_tty_file_id;

--bun:split

ALTER TABLE sessions ADD CONSTRAINT fk_sessions_tty_file_id FOREIGN KEY (tty_file_id) REFERENCES tty_files (id) ON DELETE SET NULL ON UPDATE CASCADE;

--bun:split

ALTER TABLE events DROP CONSTRAINT fk_events_sensor_id;

--bun:split

ALTER TABLE events ADD CONSTRAINT fk_events_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors (id) ON DELETE RESTRICT ON UPDATE CASCADE;

--bun:split

ALTER TABLE events DROP CONSTRAINT fk_events_session_id;

--bun:split

ALTER TABLE events ADD CONSTRAINT fk_events_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE ON UPDATE CASCADE;

--bun:split

ALTER TABLE event_data DROP CONSTRAINT fk_event_data_event_id;

--bun:split

ALTER TABLE event_data ADD CONSTRAINT fk_event_data_event_id FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE ON UPDATE CASCADE;

--bun:split

ALTER TABLE login_attempts DROP CONSTRAINT fk_login_attempts_session_id;

--bun:split

ALTER TABLE login_attempts ADD CONSTRAINT fk_login_attempts_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE ON UPDATE CASCADE;

--bun:split

ALTER TABLE commands DROP CONSTRAINT fk_commands_session_id;

--bun:split

ALTER TABLE commands ADD CONSTRAINT fk_commands_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE ON UPDATE CASCADE;

--bun:split

ALTER TABLE transfers DROP CONSTRAINT fk_transfers_session_id;

--bun:split

ALTER TABLE transfers ADD CONSTRAINT fk_transfers_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE ON UPDATE CASCADE;

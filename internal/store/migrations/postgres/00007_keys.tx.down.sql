-- Back to the names PostgreSQL chose.

ALTER TABLE sensors RENAME CONSTRAINT pk_sensors TO sensors_pkey;

--bun:split

ALTER TABLE tty_files RENAME CONSTRAINT pk_tty_files TO tty_files_pkey;

--bun:split

ALTER TABLE ingest_tokens RENAME CONSTRAINT pk_ingest_tokens TO ingest_tokens_pkey;

--bun:split

ALTER TABLE sessions RENAME CONSTRAINT pk_sessions TO sessions_pkey;

--bun:split

ALTER TABLE events RENAME CONSTRAINT pk_events TO events_pkey;

--bun:split

ALTER TABLE event_data RENAME CONSTRAINT pk_event_data TO event_data_pkey;

--bun:split

ALTER TABLE login_attempts RENAME CONSTRAINT pk_login_attempts TO login_attempts_pkey;

--bun:split

ALTER TABLE commands RENAME CONSTRAINT pk_commands TO commands_pkey;

--bun:split

ALTER TABLE transfers RENAME CONSTRAINT pk_transfers TO transfers_pkey;

--bun:split

ALTER TABLE sensors RENAME CONSTRAINT uk_sensors_name TO sensors_name_key;

--bun:split

ALTER TABLE tty_files RENAME CONSTRAINT uk_tty_files_sha256 TO tty_files_sha256_key;

--bun:split

ALTER TABLE ingest_tokens RENAME CONSTRAINT uk_ingest_tokens_token_hash TO ingest_tokens_token_hash_key;

--bun:split

ALTER TABLE sessions RENAME CONSTRAINT uk_sessions_sensor_id_cowrie_session TO sessions_sensor_id_cowrie_session_key;

--bun:split

ALTER TABLE events RENAME CONSTRAINT uk_events_hash TO events_hash_key;

--bun:split

ALTER INDEX ix_ingest_tokens_sensor_id RENAME TO ingest_tokens_sensor;

--bun:split

ALTER INDEX ix_sessions_started_at RENAME TO sessions_started_at;

--bun:split

ALTER INDEX ix_sessions_sensor_id_started_at RENAME TO sessions_sensor_started_at;

--bun:split

ALTER INDEX ix_sessions_src_ip RENAME TO sessions_src_ip;

--bun:split

ALTER INDEX ix_sessions_verdict RENAME TO sessions_verdict;

--bun:split

ALTER INDEX ix_sessions_geo_country_geo_city RENAME TO sessions_geo_place;

--bun:split

ALTER INDEX ix_sessions_asn RENAME TO sessions_asn;

--bun:split

ALTER INDEX ix_sessions_tty_file_id RENAME TO sessions_tty_file;

--bun:split

ALTER INDEX ix_sessions_shell RENAME TO sessions_shell;

--bun:split

ALTER INDEX ix_events_session_id_ts RENAME TO events_session_ts;

--bun:split

ALTER INDEX ix_events_sensor_id RENAME TO events_sensor;

--bun:split

ALTER INDEX ix_login_attempts_session_id RENAME TO login_attempts_session;

--bun:split

ALTER INDEX ix_login_attempts_ts RENAME TO login_attempts_ts;

--bun:split

ALTER INDEX ix_login_attempts_username_session_id RENAME TO login_attempts_username;

--bun:split

ALTER INDEX ix_commands_session_id RENAME TO commands_session;

--bun:split

ALTER INDEX ix_transfers_session_id RENAME TO transfers_session;

--bun:split

ALTER INDEX ix_transfers_sha256 RENAME TO transfers_sha256;

--bun:split

ALTER TABLE ingest_tokens DROP CONSTRAINT fk_ingest_tokens_sensor_id;

--bun:split

ALTER TABLE ingest_tokens ADD CONSTRAINT fk_ingest_tokens_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE sessions DROP CONSTRAINT fk_sessions_sensor_id;

--bun:split

ALTER TABLE sessions ADD CONSTRAINT fk_sessions_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors (id) ON DELETE RESTRICT;

--bun:split

ALTER TABLE sessions DROP CONSTRAINT fk_sessions_tty_file_id;

--bun:split

ALTER TABLE sessions ADD CONSTRAINT fk_sessions_tty_file_id FOREIGN KEY (tty_file_id) REFERENCES tty_files (id) ON DELETE SET NULL;

--bun:split

ALTER TABLE events DROP CONSTRAINT fk_events_sensor_id;

--bun:split

ALTER TABLE events ADD CONSTRAINT fk_events_sensor_id FOREIGN KEY (sensor_id) REFERENCES sensors (id) ON DELETE RESTRICT;

--bun:split

ALTER TABLE events DROP CONSTRAINT fk_events_session_id;

--bun:split

ALTER TABLE events ADD CONSTRAINT fk_events_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE event_data DROP CONSTRAINT fk_event_data_event_id;

--bun:split

ALTER TABLE event_data ADD CONSTRAINT fk_event_data_event_id FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE login_attempts DROP CONSTRAINT fk_login_attempts_session_id;

--bun:split

ALTER TABLE login_attempts ADD CONSTRAINT fk_login_attempts_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE commands DROP CONSTRAINT fk_commands_session_id;

--bun:split

ALTER TABLE commands ADD CONSTRAINT fk_commands_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE;

--bun:split

ALTER TABLE transfers DROP CONSTRAINT fk_transfers_session_id;

--bun:split

ALTER TABLE transfers ADD CONSTRAINT fk_transfers_session_id FOREIGN KEY (session_id) REFERENCES sessions (id) ON DELETE CASCADE;

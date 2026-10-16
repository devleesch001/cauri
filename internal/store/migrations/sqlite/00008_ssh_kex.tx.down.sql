ALTER TABLE events DROP COLUMN data;

--bun:split

DROP INDEX ix_sessions_ssh_kex_id;

--bun:split

ALTER TABLE sessions DROP COLUMN ssh_kex_id;

--bun:split

DROP TABLE ssh_kex;

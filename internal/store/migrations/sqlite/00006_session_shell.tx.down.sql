DROP INDEX sessions_shell;

--bun:split

ALTER TABLE sessions DROP COLUMN shell;

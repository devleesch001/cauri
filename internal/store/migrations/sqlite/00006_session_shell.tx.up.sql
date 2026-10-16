-- Whether a session opened an interactive shell and typed in it, as its
-- recording tells (internal/verdict): NULL without a recording to read.

ALTER TABLE sessions ADD COLUMN shell INTEGER;

--bun:split

CREATE INDEX sessions_shell ON sessions (shell);

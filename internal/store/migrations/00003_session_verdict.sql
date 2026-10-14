-- The human/bot verdict of a session (package verdict), stored to filter
-- the session list. verdict_version is the version of the criteria that
-- computed it: other versions are recomputed when the API starts.

-- +goose Up
ALTER TABLE sessions ADD COLUMN verdict TEXT;
ALTER TABLE sessions ADD COLUMN verdict_score INTEGER;
ALTER TABLE sessions ADD COLUMN verdict_version TEXT;
CREATE INDEX sessions_verdict ON sessions (verdict);

-- +goose Down
DROP INDEX sessions_verdict;
ALTER TABLE sessions DROP COLUMN verdict_version;
ALTER TABLE sessions DROP COLUMN verdict_score;
ALTER TABLE sessions DROP COLUMN verdict;

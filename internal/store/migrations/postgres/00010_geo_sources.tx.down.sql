DROP INDEX ix_sessions_geo_source_id;

--bun:split

ALTER TABLE sessions DROP COLUMN geo_source_id;

--bun:split

DROP TABLE geo_sources;

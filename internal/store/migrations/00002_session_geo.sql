-- Where a session's source address is, from the GeoIP database configured
-- at the time (geo_source names it). geo_source set with geo_country NULL
-- means the address was looked up but is unknown (private ranges).

-- +goose Up
ALTER TABLE sessions ADD COLUMN geo_country TEXT;
ALTER TABLE sessions ADD COLUMN geo_city TEXT;
ALTER TABLE sessions ADD COLUMN geo_source TEXT;
CREATE INDEX sessions_geo_country ON sessions (geo_country);

-- +goose Down
DROP INDEX sessions_geo_country;
ALTER TABLE sessions DROP COLUMN geo_source;
ALTER TABLE sessions DROP COLUMN geo_city;
ALTER TABLE sessions DROP COLUMN geo_country;

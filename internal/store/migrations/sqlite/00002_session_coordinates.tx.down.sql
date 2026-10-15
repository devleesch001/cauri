DROP INDEX sessions_geo_place;

--bun:split

CREATE INDEX sessions_geo_country ON sessions (geo_country);

--bun:split

ALTER TABLE sessions DROP COLUMN geo_lon;

--bun:split

ALTER TABLE sessions DROP COLUMN geo_lat;

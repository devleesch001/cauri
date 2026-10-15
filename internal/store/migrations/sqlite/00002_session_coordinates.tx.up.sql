-- Coordinates of the city a session comes from, from the GeoIP database
-- (the city's centre, not a precise position), to place it on the map.
-- Cities are filtered by country and name, hence the index on both, which
-- also serves the country alone.

ALTER TABLE sessions ADD COLUMN geo_lat REAL;

--bun:split

ALTER TABLE sessions ADD COLUMN geo_lon REAL;

--bun:split

DROP INDEX sessions_geo_country;

--bun:split

CREATE INDEX sessions_geo_place ON sessions (geo_country, geo_city);

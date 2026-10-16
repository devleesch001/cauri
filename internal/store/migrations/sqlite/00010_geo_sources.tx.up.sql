-- The GeoIP databases that located sessions, once each, instead of their
-- type and build date joined in a text repeated on every session.
-- Migration 00011 fills them from sessions.geo_source and drops it.

CREATE TABLE geo_sources (
    id    INTEGER NOT NULL,
    type  TEXT NOT NULL,    -- such as DBIP-City-Lite: its provider's attribution is due
    built INTEGER NOT NULL, -- build date of the database
    CONSTRAINT pk_geo_sources PRIMARY KEY (id)
);

--bun:split

CREATE UNIQUE INDEX uk_geo_sources_type_built ON geo_sources (type, built);

--bun:split

-- A database removed: its sessions are located again by the current one.
ALTER TABLE sessions ADD COLUMN geo_source_id INTEGER CONSTRAINT fk_sessions_geo_source_id REFERENCES geo_sources (id) ON DELETE SET NULL ON UPDATE CASCADE;

--bun:split

CREATE INDEX ix_sessions_geo_source_id ON sessions (geo_source_id);

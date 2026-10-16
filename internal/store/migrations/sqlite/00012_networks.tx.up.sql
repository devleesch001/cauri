-- The networks (autonomous systems) sessions come from, once each: the
-- organisation depended on the number only, yet was repeated on every
-- session. Its name is the latest an ASN database gave.

CREATE TABLE networks (
    id  INTEGER NOT NULL,
    asn INTEGER NOT NULL, -- autonomous system number
    org TEXT,             -- its organisation
    CONSTRAINT pk_networks PRIMARY KEY (id)
);

--bun:split

CREATE UNIQUE INDEX uk_networks_asn ON networks (asn);

--bun:split

-- A network removed: its sessions are found again by the ASN database loaded.
ALTER TABLE sessions ADD COLUMN network_id INTEGER CONSTRAINT fk_sessions_network_id REFERENCES networks (id) ON DELETE SET NULL ON UPDATE CASCADE;

--bun:split

CREATE INDEX ix_sessions_network_id ON sessions (network_id);

--bun:split

INSERT INTO networks (asn, org) SELECT asn, MAX(as_org) FROM sessions WHERE asn IS NOT NULL GROUP BY asn;

--bun:split

UPDATE sessions SET network_id = (SELECT n.id FROM networks AS n WHERE n.asn = sessions.asn) WHERE asn IS NOT NULL;

--bun:split

DROP INDEX ix_sessions_asn;

--bun:split

ALTER TABLE sessions DROP COLUMN as_org;

--bun:split

ALTER TABLE sessions DROP COLUMN asn;

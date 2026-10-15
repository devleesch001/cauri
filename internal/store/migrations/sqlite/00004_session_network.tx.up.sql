-- The network (autonomous system) a session comes from, from an optional
-- ASN database: who hosts the attacking machines.

ALTER TABLE sessions ADD COLUMN asn INTEGER;

--bun:split

ALTER TABLE sessions ADD COLUMN as_org TEXT;

--bun:split

CREATE INDEX sessions_asn ON sessions (asn);

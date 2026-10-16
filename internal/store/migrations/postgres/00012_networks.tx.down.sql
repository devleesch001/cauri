ALTER TABLE sessions ADD COLUMN asn BIGINT;

--bun:split

ALTER TABLE sessions ADD COLUMN as_org TEXT;

--bun:split

CREATE INDEX ix_sessions_asn ON sessions (asn);

--bun:split

UPDATE sessions SET asn = (SELECT n.asn FROM networks AS n WHERE n.id = sessions.network_id),
    as_org = (SELECT n.org FROM networks AS n WHERE n.id = sessions.network_id)
WHERE network_id IS NOT NULL;

--bun:split

DROP INDEX ix_sessions_network_id;

--bun:split

ALTER TABLE sessions DROP COLUMN network_id;

--bun:split

DROP TABLE networks;

DROP INDEX sessions_asn;

--bun:split

ALTER TABLE sessions DROP COLUMN as_org;

--bun:split

ALTER TABLE sessions DROP COLUMN asn;

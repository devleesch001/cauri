-- The payloads page lists files by digest and leads to the sessions that
-- transferred one: both look transfers up by sha256.

CREATE INDEX transfers_sha256 ON transfers (sha256);

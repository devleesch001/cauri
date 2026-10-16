-- What the raw lines alone held, projected so that they can be purged:
-- the key exchange offers of SSH clients, once per combination (ssh_kex,
-- see cowrie.Kex), and what the tables do not hold of each event
-- (events.data, see cowrie.Fragment). Migration 00009 fills them from the
-- raw lines already stored.

CREATE TABLE ssh_kex (
    id         INTEGER NOT NULL,
    digest     BLOB NOT NULL, -- SHA-256 of every list the client offered: one row per combination
    hassh      TEXT,          -- Cowrie's client fingerprint, of the algorithms only
    algorithms TEXT,          -- what the HASSH hashes: key exchange;encryption;MAC;compression
    key_algs   TEXT,          -- host key algorithms, not in the HASSH
    lang_algs  TEXT,
    CONSTRAINT pk_ssh_kex PRIMARY KEY (id)
);

--bun:split

CREATE UNIQUE INDEX uk_ssh_kex_digest ON ssh_kex (digest);

--bun:split

CREATE INDEX ix_ssh_kex_hassh ON ssh_kex (hassh);

--bun:split

-- Key exchanges are never deleted: RESTRICT only guards against a mistake.
ALTER TABLE sessions ADD COLUMN ssh_kex_id INTEGER CONSTRAINT fk_sessions_ssh_kex_id REFERENCES ssh_kex (id) ON DELETE RESTRICT;

--bun:split

CREATE INDEX ix_sessions_ssh_kex_id ON sessions (ssh_kex_id);

--bun:split

ALTER TABLE events ADD COLUMN data TEXT;

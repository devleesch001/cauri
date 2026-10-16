-- The raw Cowrie lines, renamed so that they are not mistaken for
-- events.data, what the tables do not hold of a line. SQLite renames no
-- constraint: the table is rebuilt under its new name, keys included;
-- nothing refers to it, so dropping the old one touches nothing else.

CREATE TABLE event_raw_data (
    event_id INTEGER NOT NULL,
    data     TEXT NOT NULL, -- the Cowrie line; can be purged, or not stored
    CONSTRAINT pk_event_raw_data PRIMARY KEY (event_id),
    CONSTRAINT fk_event_raw_data_event_id FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE ON UPDATE CASCADE
);

--bun:split

INSERT INTO event_raw_data (event_id, data) SELECT event_id, data FROM event_data;

--bun:split

DROP TABLE event_data;

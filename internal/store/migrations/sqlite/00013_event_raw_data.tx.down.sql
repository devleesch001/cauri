CREATE TABLE event_data (
    event_id INTEGER NOT NULL,
    data     TEXT NOT NULL, -- the Cowrie line; can be purged
    CONSTRAINT pk_event_data PRIMARY KEY (event_id),
    CONSTRAINT fk_event_data_event_id FOREIGN KEY (event_id) REFERENCES events (id) ON DELETE CASCADE ON UPDATE CASCADE
);

--bun:split

INSERT INTO event_data (event_id, data) SELECT event_id, data FROM event_raw_data;

--bun:split

DROP TABLE event_raw_data;

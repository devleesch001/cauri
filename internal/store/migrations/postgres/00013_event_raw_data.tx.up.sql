-- The raw Cowrie lines, renamed so that they are not mistaken for
-- events.data, what the tables do not hold of a line.

ALTER TABLE event_data RENAME TO event_raw_data;

--bun:split

ALTER TABLE event_raw_data RENAME CONSTRAINT pk_event_data TO pk_event_raw_data;

--bun:split

ALTER TABLE event_raw_data RENAME CONSTRAINT fk_event_data_event_id TO fk_event_raw_data_event_id;

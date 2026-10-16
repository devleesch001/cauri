ALTER TABLE event_raw_data RENAME CONSTRAINT fk_event_raw_data_event_id TO fk_event_data_event_id;

--bun:split

ALTER TABLE event_raw_data RENAME CONSTRAINT pk_event_raw_data TO pk_event_data;

--bun:split

ALTER TABLE event_raw_data RENAME TO event_data;

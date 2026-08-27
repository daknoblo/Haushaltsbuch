-- A booking created by a script needs a name the caller chose, or a job that
-- runs twice files everything twice. The id is the caller's own and only has to
-- be unique inside one household; bookings entered by hand carry none, so the
-- index has to ignore the empty string.
ALTER TABLE bookings ADD COLUMN external_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX idx_bookings_external
    ON bookings(household_id, external_id)
    WHERE external_id <> '';

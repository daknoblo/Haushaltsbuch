-- Not every booking belongs in the settlement: a savings rate everyone runs
-- for themselves moves no money between the members and would only make the
-- balance look wrong.
ALTER TABLE bookings ADD COLUMN settle INTEGER NOT NULL DEFAULT 1;

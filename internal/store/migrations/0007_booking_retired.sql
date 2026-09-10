-- Price-change predecessors remain historical bookings, but must never be
-- extended by the annual carry. Existing rows have no reliable lineage.
ALTER TABLE bookings ADD COLUMN retired INTEGER NOT NULL DEFAULT 0 CHECK (retired IN (0, 1));

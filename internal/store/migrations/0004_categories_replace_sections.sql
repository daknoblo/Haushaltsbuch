-- Areas are gone: a booking's category already says what kind of cost it is, so
-- the second grouping only duplicated it. The bookings table has to be rebuilt
-- because SQLite refuses to drop a column that carries a foreign key.

-- A category carries a small symbol so long lists stay scannable.
ALTER TABLE categories ADD COLUMN icon TEXT NOT NULL DEFAULT '';

CREATE TABLE bookings_new (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id    INTEGER NOT NULL REFERENCES households(id)  ON DELETE CASCADE,
    category_id     INTEGER NOT NULL REFERENCES categories(id),
    -- Who fronts the money. Without it there is no way to say who owes whom.
    payer_member_id INTEGER          REFERENCES members(id)     ON DELETE SET NULL,
    direction       TEXT    NOT NULL DEFAULT 'expense',  -- income|expense
    name            TEXT    NOT NULL,
    note            TEXT    NOT NULL DEFAULT '',
    amount_cents    INTEGER NOT NULL DEFAULT 0,
    frequency       TEXT    NOT NULL DEFAULT 'monthly',
    interval_n      INTEGER NOT NULL DEFAULT 1,
    -- Where inside the month a recurring booking falls: start|mid|end.
    due_point       TEXT    NOT NULL DEFAULT 'start',
    starts_on       TEXT    NOT NULL DEFAULT '',
    ends_on         TEXT    NOT NULL DEFAULT '',
    cost_nature     TEXT    NOT NULL DEFAULT 'fix',
    budget_class    TEXT    NOT NULL DEFAULT 'need',
    split_mode      TEXT    NOT NULL DEFAULT 'equal',
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL
);

-- Existing bookings are assigned to the first person of their household; the
-- payer is a new fact nobody could have recorded before.
INSERT INTO bookings_new (
    id, household_id, category_id, payer_member_id, direction, name, note,
    amount_cents, frequency, interval_n, due_point, starts_on, ends_on,
    cost_nature, budget_class, split_mode, created_at, updated_at
)
SELECT
    b.id, b.household_id, b.category_id,
    (SELECT m.id FROM members m
     WHERE m.household_id = b.household_id
     ORDER BY m.sort_order, m.id LIMIT 1),
    b.direction, b.name, b.note, b.amount_cents, b.frequency, b.interval_n,
    'start', b.starts_on, b.ends_on, b.cost_nature, b.budget_class,
    b.split_mode, b.created_at, b.updated_at
FROM bookings b;

DROP TABLE bookings;
ALTER TABLE bookings_new RENAME TO bookings;

CREATE INDEX idx_bookings_household ON bookings(household_id, direction);
CREATE INDEX idx_bookings_category ON bookings(category_id);
CREATE INDEX idx_bookings_payer ON bookings(payer_member_id);

DROP TABLE sections;

-- A recurring amount can differ for a while, e.g. an introductory price for the
-- first six months. An override wins for every month it covers.
CREATE TABLE booking_overrides (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    booking_id   INTEGER NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    starts_on    TEXT    NOT NULL DEFAULT '',  -- YYYY-MM-DD, empty = open start
    ends_on      TEXT    NOT NULL DEFAULT '',  -- YYYY-MM-DD, empty = open end
    amount_cents INTEGER NOT NULL DEFAULT 0,
    note         TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX idx_booking_overrides_booking ON booking_overrides(booking_id);

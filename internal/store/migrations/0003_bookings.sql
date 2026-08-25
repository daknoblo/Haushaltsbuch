-- Unifies expenses and incomes into a single bookings table so that a figure is
-- maintained in exactly one place, and adds tags plus classified categories.

-- A category is mandatory on a booking from now on. The classification keeps
-- income categories out of expense pickers and vice versa.
ALTER TABLE categories ADD COLUMN classification TEXT NOT NULL DEFAULT 'expense';
ALTER TABLE categories ADD COLUMN color TEXT NOT NULL DEFAULT '';
ALTER TABLE categories ADD COLUMN sort_order INTEGER NOT NULL DEFAULT 0;

CREATE TABLE tags (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    color        TEXT    NOT NULL DEFAULT '',
    UNIQUE(household_id, name)
);
CREATE INDEX idx_tags_household ON tags(household_id);

CREATE TABLE bookings (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id INTEGER NOT NULL REFERENCES households(id)  ON DELETE CASCADE,
    category_id  INTEGER NOT NULL REFERENCES categories(id),
    section_id   INTEGER          REFERENCES sections(id)    ON DELETE SET NULL,
    direction    TEXT    NOT NULL DEFAULT 'expense',  -- income|expense
    name         TEXT    NOT NULL,
    note         TEXT    NOT NULL DEFAULT '',
    amount_cents INTEGER NOT NULL DEFAULT 0,
    -- once|weekly|monthly|quarterly|yearly; every recurring amount is spread
    -- across the months it covers, so a yearly premium shows up as 1/12.
    frequency    TEXT    NOT NULL DEFAULT 'monthly',
    interval_n   INTEGER NOT NULL DEFAULT 1,
    starts_on    TEXT    NOT NULL DEFAULT '',         -- YYYY-MM-DD, the date for 'once'
    ends_on      TEXT    NOT NULL DEFAULT '',         -- YYYY-MM-DD, empty = open ended
    cost_nature  TEXT    NOT NULL DEFAULT 'fix',      -- fix|variable
    budget_class TEXT    NOT NULL DEFAULT 'need',     -- need|want|saving
    split_mode   TEXT    NOT NULL DEFAULT 'equal',    -- equal|percent|fixed
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL
);
CREATE INDEX idx_bookings_household ON bookings(household_id, direction);
CREATE INDEX idx_bookings_category ON bookings(category_id);
CREATE INDEX idx_bookings_section ON bookings(section_id);

CREATE TABLE booking_splits (
    booking_id INTEGER NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    member_id  INTEGER NOT NULL REFERENCES members(id)  ON DELETE CASCADE,
    value      REAL    NOT NULL DEFAULT 0, -- percent (0-100) or cents, per split_mode
    PRIMARY KEY (booking_id, member_id)
);

CREATE TABLE booking_tags (
    booking_id INTEGER NOT NULL REFERENCES bookings(id) ON DELETE CASCADE,
    tag_id     INTEGER NOT NULL REFERENCES tags(id)     ON DELETE CASCADE,
    PRIMARY KEY (booking_id, tag_id)
);
CREATE INDEX idx_booking_tags_tag ON booking_tags(tag_id);

-- Expenses without a category need a home, and incomes need a category at all.
INSERT INTO categories (household_id, name, classification)
SELECT h.id, 'Sonstiges', 'expense' FROM households h
WHERE NOT EXISTS (
    SELECT 1 FROM categories c WHERE c.household_id = h.id AND c.name = 'Sonstiges'
);
INSERT INTO categories (household_id, name, classification)
SELECT h.id, 'Einkommen', 'income' FROM households h
WHERE NOT EXISTS (
    SELECT 1 FROM categories c WHERE c.household_id = h.id AND c.name = 'Einkommen'
);

-- Expense ids are carried over unchanged so the splits map across untouched.
INSERT INTO bookings (
    id, household_id, category_id, section_id, direction, name, amount_cents,
    frequency, interval_n, starts_on, ends_on, cost_nature, budget_class,
    split_mode, sort_order, created_at, updated_at
)
SELECT
    e.id,
    e.household_id,
    COALESCE(e.category_id, (
        SELECT c.id FROM categories c
        WHERE c.household_id = e.household_id AND c.name = 'Sonstiges'
    )),
    e.section_id,
    'expense',
    e.name,
    e.amount_cents,
    CASE WHEN e.is_oneoff = 1 THEN 'once' ELSE e.frequency END,
    1,
    CASE
        WHEN e.is_oneoff = 1        THEN e.occurred_on
        WHEN e.active_from <> ''    THEN e.active_from || '-01'
        ELSE ''
    END,
    CASE
        WHEN e.is_oneoff = 1        THEN ''
        WHEN e.active_until <> ''   THEN e.active_until || '-01'
        ELSE ''
    END,
    e.cost_nature,
    e.budget_class,
    e.split_mode,
    e.sort_order,
    e.created_at,
    e.updated_at
FROM expenses e;

INSERT INTO booking_splits (booking_id, member_id, value)
SELECT s.expense_id, s.member_id, s.value FROM expense_splits s;

-- Income ids are offset past the highest expense id so both stay collision free
-- without needing a lookup table.
INSERT INTO bookings (
    id, household_id, category_id, section_id, direction, name, amount_cents,
    frequency, interval_n, starts_on, ends_on, cost_nature, budget_class,
    split_mode, sort_order, created_at, updated_at
)
SELECT
    (SELECT COALESCE(MAX(id), 0) FROM expenses) + i.id,
    i.household_id,
    (SELECT c.id FROM categories c
     WHERE c.household_id = i.household_id AND c.name = 'Einkommen'),
    NULL,
    'income',
    CASE WHEN i.name = '' THEN 'Einkommen' ELSE i.name END,
    i.amount_cents,
    'once',
    1,
    i.year_month || '-01',
    '',
    'fix',
    'need',
    'percent',
    i.sort_order,
    i.created_at,
    i.updated_at
FROM incomes i;

-- An income belonged to exactly one member, which is a 100 % percent split.
INSERT INTO booking_splits (booking_id, member_id, value)
SELECT (SELECT COALESCE(MAX(id), 0) FROM expenses) + i.id, i.member_id, 100
FROM incomes i;

DROP TABLE expense_splits;
DROP TABLE expenses;
DROP TABLE incomes;

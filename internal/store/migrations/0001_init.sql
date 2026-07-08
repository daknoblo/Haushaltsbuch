-- Initial schema for Haushaltsbuch.

CREATE TABLE households (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL
);

CREATE TABLE members (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    color        TEXT    NOT NULL DEFAULT '',
    sort_order   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_members_household ON members(household_id);

CREATE TABLE sections (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL,
    sort_order   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_sections_household ON sections(household_id);

CREATE TABLE categories (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    name         TEXT    NOT NULL
);
CREATE INDEX idx_categories_household ON categories(household_id);

CREATE TABLE expenses (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    section_id   INTEGER REFERENCES sections(id)   ON DELETE SET NULL,
    category_id  INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    name         TEXT    NOT NULL,
    amount_cents INTEGER NOT NULL DEFAULT 0,
    frequency    TEXT    NOT NULL DEFAULT 'monthly', -- weekly|monthly|yearly
    cost_nature  TEXT    NOT NULL DEFAULT 'fix',      -- fix|variable
    budget_class TEXT    NOT NULL DEFAULT 'need',     -- need|want|saving
    is_oneoff    INTEGER NOT NULL DEFAULT 0,
    occurred_on  TEXT    NOT NULL DEFAULT '',         -- YYYY-MM-DD for one-offs
    active_from  TEXT    NOT NULL DEFAULT '',         -- YYYY-MM start (recurring)
    active_until TEXT    NOT NULL DEFAULT '',         -- YYYY-MM end (recurring, optional)
    split_mode   TEXT    NOT NULL DEFAULT 'equal',    -- equal|percent|fixed
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL
);
CREATE INDEX idx_expenses_household ON expenses(household_id);
CREATE INDEX idx_expenses_section ON expenses(section_id);

CREATE TABLE expense_splits (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    expense_id INTEGER NOT NULL REFERENCES expenses(id) ON DELETE CASCADE,
    member_id  INTEGER NOT NULL REFERENCES members(id)  ON DELETE CASCADE,
    value      REAL    NOT NULL DEFAULT 0, -- percent (0-100) or fixed cents by split_mode
    UNIQUE(expense_id, member_id)
);
CREATE INDEX idx_splits_expense ON expense_splits(expense_id);

CREATE TABLE incomes (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    member_id    INTEGER NOT NULL REFERENCES members(id)   ON DELETE CASCADE,
    year_month   TEXT    NOT NULL,                 -- YYYY-MM
    name         TEXT    NOT NULL DEFAULT '',
    amount_cents INTEGER NOT NULL DEFAULT 0,
    sort_order   INTEGER NOT NULL DEFAULT 0,
    created_at   TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL
);
CREATE INDEX idx_incomes_lookup ON incomes(household_id, member_id, year_month);

CREATE TABLE app_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

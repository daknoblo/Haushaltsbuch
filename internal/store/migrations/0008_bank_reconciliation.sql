CREATE TABLE bank_accounts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    member_id INTEGER NOT NULL REFERENCES members(id),
    name TEXT NOT NULL,
    UNIQUE(household_id, name)
);

CREATE TABLE bank_imports (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    account_id INTEGER NOT NULL REFERENCES bank_accounts(id) ON DELETE CASCADE,
    digest TEXT NOT NULL,
    filename TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE(account_id, digest)
);

CREATE TABLE bank_transactions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    import_id INTEGER NOT NULL REFERENCES bank_imports(id) ON DELETE CASCADE,
    booked_on TEXT NOT NULL,
    direction TEXT NOT NULL CHECK(direction IN ('income', 'expense')),
    amount_cents INTEGER NOT NULL CHECK(amount_cents >= 0 AND amount_cents <= 1000000000000),
    name TEXT NOT NULL,
    reference TEXT NOT NULL,
    fingerprint TEXT NOT NULL,
    decision TEXT NOT NULL DEFAULT 'pending' CHECK(decision IN ('pending', 'accepted', 'ignored', 'transfer')),
    category_id INTEGER REFERENCES categories(id),
    plan_id INTEGER REFERENCES bookings(id) ON DELETE SET NULL,
    transfer_account_id INTEGER REFERENCES bank_accounts(id),
    cost_nature TEXT NOT NULL DEFAULT 'variable' CHECK(cost_nature IN ('fix', 'variable')),
    budget_class TEXT NOT NULL DEFAULT 'need' CHECK(budget_class IN ('need', 'want', 'saving')),
    settle INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX idx_bank_transactions_month ON bank_transactions(household_id, booked_on);
CREATE INDEX idx_bank_transactions_fingerprint ON bank_transactions(household_id, fingerprint);

CREATE TABLE bank_splits (
    transaction_id INTEGER NOT NULL REFERENCES bank_transactions(id) ON DELETE CASCADE,
    member_id INTEGER NOT NULL REFERENCES members(id),
    cents INTEGER NOT NULL CHECK(cents >= 0),
    PRIMARY KEY(transaction_id, member_id)
);

CREATE TABLE bank_tags (
    transaction_id INTEGER NOT NULL REFERENCES bank_transactions(id) ON DELETE CASCADE,
    tag_id INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY(transaction_id, tag_id)
);

CREATE TABLE reconciled_months (
    household_id INTEGER NOT NULL REFERENCES households(id) ON DELETE CASCADE,
    month TEXT NOT NULL,
    PRIMARY KEY(household_id, month)
);

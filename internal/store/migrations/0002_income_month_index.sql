-- Supports listing all income lines of a household for a month or a month
-- range without scanning the whole table. The existing lookup index starts with
-- member_id, which cannot serve a range scan over year_month.
CREATE INDEX IF NOT EXISTS idx_incomes_household_month ON incomes(household_id, year_month);

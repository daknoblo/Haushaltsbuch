ALTER TABLE bank_accounts ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0 CHECK(is_default IN (0, 1));
CREATE UNIQUE INDEX idx_bank_account_default ON bank_accounts(member_id) WHERE is_default = 1;

-- A member-specific base prevents collisions between newly generated accounts.
-- Existing user-assigned names still take precedence, including numbered names.
WITH RECURSIVE candidates(household_id, member_id, base, n, name) AS (
    SELECT m.household_id, m.id, 'Girokonto (' || m.id || ')', 0, 'Girokonto (' || m.id || ')'
    FROM members m
    WHERE NOT EXISTS (SELECT 1 FROM bank_accounts a WHERE a.member_id = m.id AND a.household_id = m.household_id)
    UNION ALL
    SELECT household_id, member_id, base, n + 1, base || ' #' || (n + 1)
    FROM candidates c
    WHERE EXISTS (SELECT 1 FROM bank_accounts a WHERE a.household_id = c.household_id AND a.name = c.name)
)
INSERT INTO bank_accounts (household_id, member_id, name)
SELECT household_id, member_id, name FROM candidates c
WHERE NOT EXISTS (SELECT 1 FROM bank_accounts a WHERE a.household_id = c.household_id AND a.name = c.name);

UPDATE bank_accounts SET is_default = 1
WHERE id IN (SELECT MIN(id) FROM bank_accounts GROUP BY household_id, member_id);

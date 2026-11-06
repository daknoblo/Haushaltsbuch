package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Bank errors distinguish review conflicts from persistence failures.
var (
	ErrBankMemberInUse    = errors.New("store: member needed by bank ledger")
	ErrBankHistoryInUse   = errors.New("store: accepted payments or account history must be retained")
	ErrDefaultBankAccount = errors.New("store: default bank account must be retained")
	ErrBankAccountName    = errors.New("store: bank account name already in use")
	ErrDuplicateImport    = errors.New("store: statement already imported")
	ErrDuplicatePayment   = errors.New("store: possible duplicate payment")
	ErrClosedMonth        = errors.New("store: month is closed")
	ErrPendingPayments    = errors.New("store: month has pending payments")
)

// BankAccount identifies a statement source and the member fronting its payments.
type BankAccount struct {
	ID       int64
	MemberID int64
	Name     string
	Default  bool
}

// BankImport retains provenance and a document digest, never the PDF itself.
type BankImport struct {
	ID        int64
	AccountID int64
	Digest    string
	Filename  string
	CreatedAt string
}

// BankSplit snapshots an actual payment allocation in integer cents.
type BankSplit struct {
	MemberID int64
	Cents    int64
}

// BankTransaction is a statement row, not a recurring plan. Accepted allocations
// are snapshots: a later plan edit must not rewrite actual household history.
type BankTransaction struct {
	ID                int64
	ImportID          int64
	BookedOn          string
	Direction         Direction
	AmountCents       int64
	Name              string
	Reference         string
	Fingerprint       string
	SourceFingerprint string        `json:",omitempty"`
	Analysis          *BankAnalysis `json:",omitempty"`
	Decision          string
	CategoryID        int64
	PlanID            int64
	TransferAccountID int64
	CostNature        CostNature
	BudgetClass       BudgetClass
	Settle            bool
	Splits            []BankSplit
	TagIDs            []int64
}

// BankLedger is the complete household-scoped reconciliation snapshot.
type BankLedger struct {
	Accounts     []BankAccount
	Imports      []BankImport
	Transactions []BankTransaction
	ClosedMonths []string
}

// CreateBankAccount requires an owner belonging to the same household.
func (s *Store) CreateBankAccount(ctx context.Context, householdID int64, account BankAccount) error {
	if strings.TrimSpace(account.Name) == "" || utf8.RuneCountInString(account.Name) > 120 {
		return ErrInvalid
	}
	return s.withTx(ctx, func(tx *Store) error {
		if err := tx.checkBankAccountName(ctx, householdID, 0, strings.TrimSpace(account.Name)); err != nil {
			return err
		}
		if err := affected(tx.q.ExecContext(ctx, `INSERT INTO bank_accounts (household_id, member_id, name)
			SELECT ?, id, ? FROM members WHERE id = ? AND household_id = ?`,
			householdID, strings.TrimSpace(account.Name), account.MemberID, householdID)); err != nil {
			return err
		}
		return tx.ensureMemberBankAccount(ctx, householdID, account.MemberID)
	})
}

// DeleteBankAccount permits correcting unused account setup without erasing history.
func (s *Store) DeleteBankAccount(ctx context.Context, householdID, id int64) error {
	return s.withTx(ctx, func(tx *Store) error {
		var count int
		if err := tx.q.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM bank_imports WHERE household_id = ? AND account_id = ?) +
			(SELECT COUNT(*) FROM bank_transactions WHERE household_id = ? AND transfer_account_id = ?)`,
			householdID, id, householdID, id).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return ErrBankHistoryInUse
		}
		var isDefault bool
		err := tx.q.QueryRowContext(ctx, `SELECT is_default FROM bank_accounts WHERE id = ? AND household_id = ?`,
			id, householdID).Scan(&isDefault)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if isDefault {
			return ErrDefaultBankAccount
		}
		return affected(tx.q.ExecContext(ctx, `DELETE FROM bank_accounts WHERE id = ? AND household_id = ?`, id, householdID))
	})
}

// DiscardBankImport is a destructive maintenance primitive, not file removal.
// The UI does not expose it; actual history and closed months remain protected.
func (s *Store) DiscardBankImport(ctx context.Context, householdID, id int64) error {
	return s.withTx(ctx, func(tx *Store) error {
		l, err := tx.BankLedger(ctx, householdID)
		if err != nil {
			return err
		}
		for _, p := range l.Transactions {
			if p.ImportID != id {
				continue
			}
			if p.Decision == "accepted" {
				return ErrBankHistoryInUse
			}
			if err := tx.ensureOpenMonth(ctx, householdID, p.BookedOn[:7]); err != nil {
				return err
			}
		}
		return affected(tx.q.ExecContext(ctx, `DELETE FROM bank_imports WHERE id = ? AND household_id = ?`, id, householdID))
	})
}

// BankLedger reads all parts under one transaction, including month closure.
func (s *Store) BankLedger(ctx context.Context, householdID int64) (BankLedger, error) {
	var ledger BankLedger
	err := s.withTx(ctx, func(tx *Store) error {
		return tx.readBankLedger(ctx, householdID, &ledger)
	})
	return ledger, err
}

func (s *Store) readBankLedger(ctx context.Context, householdID int64, l *BankLedger) error {
	var err error
	l.Accounts, err = s.ListBankAccounts(ctx, householdID)
	if err != nil {
		return err
	}
	queries := []struct {
		sql  string
		scan func(*sql.Rows) error
	}{
		{`SELECT id, account_id, digest, filename, created_at FROM bank_imports WHERE household_id = ? ORDER BY id`, func(r *sql.Rows) error {
			var b BankImport
			if err := r.Scan(&b.ID, &b.AccountID, &b.Digest, &b.Filename, &b.CreatedAt); err != nil {
				return err
			}
			l.Imports = append(l.Imports, b)
			return nil
		}},
		{`SELECT id, import_id, booked_on, direction, amount_cents, name, reference, fingerprint,
			decision, COALESCE(category_id,0), COALESCE(plan_id,0), COALESCE(transfer_account_id,0),
			cost_nature, budget_class, settle, source_fingerprint, analysis FROM bank_transactions WHERE household_id = ? ORDER BY booked_on, id`, func(r *sql.Rows) error {
			var b BankTransaction
			var analysis string
			if err := r.Scan(&b.ID, &b.ImportID, &b.BookedOn, &b.Direction, &b.AmountCents, &b.Name,
				&b.Reference, &b.Fingerprint, &b.Decision, &b.CategoryID, &b.PlanID, &b.TransferAccountID,
				&b.CostNature, &b.BudgetClass, &b.Settle, &b.SourceFingerprint, &analysis); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(analysis), &b.Analysis); err != nil {
				return fmt.Errorf("read bank analysis: %w", err)
			}
			l.Transactions = append(l.Transactions, b)
			return nil
		}},
		{`SELECT month FROM reconciled_months WHERE household_id = ? ORDER BY month`, func(r *sql.Rows) error {
			var month string
			if err := r.Scan(&month); err != nil {
				return err
			}
			l.ClosedMonths = append(l.ClosedMonths, month)
			return nil
		}},
	}
	for _, q := range queries {
		rows, err := s.q.QueryContext(ctx, q.sql, householdID)
		if err != nil {
			return err
		}
		for rows.Next() {
			if err := q.scan(rows); err != nil {
				_ = rows.Close()
				return err
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
	byID := make(map[int64]*BankTransaction, len(l.Transactions))
	for i := range l.Transactions {
		byID[l.Transactions[i].ID] = &l.Transactions[i]
	}
	rows, err := s.q.QueryContext(ctx, `SELECT sp.transaction_id, sp.member_id, sp.cents FROM bank_splits sp
		JOIN bank_transactions t ON t.id = sp.transaction_id WHERE t.household_id = ? ORDER BY sp.member_id`, householdID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var sp BankSplit
		if err := rows.Scan(&id, &sp.MemberID, &sp.Cents); err != nil {
			_ = rows.Close()
			return err
		}
		byID[id].Splits = append(byID[id].Splits, sp)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	rows, err = s.q.QueryContext(ctx, `SELECT bt.transaction_id, bt.tag_id FROM bank_tags bt
		JOIN bank_transactions t ON t.id = bt.transaction_id WHERE t.household_id = ? ORDER BY bt.tag_id`, householdID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, tag int64
		if err := rows.Scan(&id, &tag); err != nil {
			return err
		}
		byID[id].TagIDs = append(byID[id].TagIDs, tag)
	}
	return rows.Err()
}

// PaymentFingerprint normalizes spacing and case without merging repeated rows.
func PaymentFingerprint(b BankTransaction) string {
	normalize := func(v string) string { return strings.Join(strings.Fields(strings.ToLower(v)), " ") }
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%s|%s",
		b.BookedOn, b.Direction, b.AmountCents, normalize(b.Name), normalize(b.Reference))))
	return hex.EncodeToString(sum[:])
}

// ValidateBankTransaction checks the scalar input before any database mutation.
func ValidateBankTransaction(b BankTransaction) error {
	if b.BookedOn == "" || ValidateDateRange(b.BookedOn, "") != nil || b.BookedOn > time.Now().Format("2006-01-02") ||
		!b.Direction.Valid() || b.AmountCents < 0 || b.AmountCents > 1_000_000_000_000 ||
		strings.TrimSpace(b.Name) == "" || utf8.RuneCountInString(b.Name) > 120 ||
		utf8.RuneCountInString(b.Reference) > 2000 ||
		!b.CostNature.Valid() || !b.BudgetClass.Valid() {
		return ErrInvalid
	}
	switch b.Decision {
	case "pending", "accepted", "ignored", "transfer":
		return nil
	default:
		return ErrInvalid
	}
}

func (s *Store) ensureOpenMonth(ctx context.Context, householdID int64, month string) error {
	var closed int
	if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM reconciled_months WHERE household_id = ? AND month = ?`,
		householdID, month).Scan(&closed); err != nil {
		return err
	}
	if closed != 0 {
		return ErrClosedMonth
	}
	return nil
}

// HasBankImport finds existing provenance, not a reason to skip reconciliation.
func (s *Store) HasBankImport(ctx context.Context, householdID, accountID int64, digest string) (bool, error) {
	var count int
	err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM bank_imports
		WHERE household_id = ? AND account_id = ? AND digest = ?`, householdID, accountID, digest).Scan(&count)
	return count > 0, err
}

// CreateBankImport persists all suggestions atomically, initially unapproved.
// User uploads use ReconcileBankImports instead of this strict creation primitive.
func (s *Store) CreateBankImport(ctx context.Context, householdID int64, batch BankImport, payments []BankTransaction) error {
	digest, err := hex.DecodeString(batch.Digest)
	if err != nil || len(digest) != sha256.Size || len(payments) == 0 || len(payments) > 1000 ||
		batch.Filename == "" || utf8.RuneCountInString(batch.Filename) > 255 {
		return ErrInvalid
	}
	return s.withTx(ctx, func(tx *Store) error {
		exists, err := tx.HasBankImport(ctx, householdID, batch.AccountID, batch.Digest)
		if err != nil {
			return err
		}
		if exists {
			return ErrDuplicateImport
		}
		res, err := tx.q.ExecContext(ctx, `INSERT INTO bank_imports (household_id, account_id, digest, filename, created_at)
			SELECT ?, id, ?, ?, ? FROM bank_accounts WHERE id = ? AND household_id = ?`,
			householdID, batch.Digest, batch.Filename, now(), batch.AccountID, householdID)
		if err := affected(res, err); err != nil {
			return err
		}
		batchID, err := res.LastInsertId()
		if err != nil {
			return err
		}
		for _, b := range payments {
			b.ID, b.ImportID, b.Decision = 0, batchID, "pending"
			b.Splits, b.TagIDs = nil, nil
			b.SourceFingerprint = BankSourceFingerprint(b)
			if err := ValidateBankTransaction(b); err != nil {
				return err
			}
			if err := tx.ensureOpenMonth(ctx, householdID, b.BookedOn[:7]); err != nil {
				return err
			}
			if err := tx.validateBankReferences(ctx, householdID, b); err != nil {
				return err
			}
			b.Analysis = nil
			if err := tx.insertBankTransaction(ctx, householdID, &b); err != nil {
				return err
			}
		}
		return nil
	})
}

func optionalID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func (s *Store) insertBankTransaction(ctx context.Context, householdID int64, b *BankTransaction) error {
	analysis, err := json.Marshal(b.Analysis)
	if err != nil {
		return err
	}
	res, err := s.q.ExecContext(ctx, `INSERT INTO bank_transactions
		(id, household_id, import_id, booked_on, direction, amount_cents, name, reference, fingerprint,
		decision, category_id, plan_id, transfer_account_id, cost_nature, budget_class, settle, source_fingerprint, analysis)
		VALUES (?, ?, (SELECT id FROM bank_imports WHERE id = ? AND household_id = ?),
		?, ?, ?, ?, ?, ?, ?, `+categoryRef+`,
		(SELECT id FROM bookings WHERE id = ? AND household_id = ?),
		(SELECT id FROM bank_accounts WHERE id = ? AND household_id = ?), ?, ?, ?, ?, ?)`,
		optionalID(b.ID), householdID, b.ImportID, householdID, b.BookedOn, b.Direction, b.AmountCents,
		b.Name, b.Reference, PaymentFingerprint(*b), b.Decision, b.CategoryID, householdID,
		b.PlanID, householdID, b.TransferAccountID, householdID, b.CostNature, b.BudgetClass, b.Settle, b.SourceFingerprint, string(analysis))
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	b.ID = id
	return s.replaceBankAllocation(ctx, householdID, id, *b)
}

func (s *Store) validateBankReferences(ctx context.Context, householdID int64, b BankTransaction) error {
	for _, ref := range []struct {
		table string
		id    int64
	}{
		{"bank_imports", b.ImportID}, {"categories", b.CategoryID},
		{"bookings", b.PlanID}, {"bank_accounts", b.TransferAccountID},
	} {
		if ref.id == 0 {
			continue
		}
		var count int
		// Table names are fixed above, never request data.
		if err := s.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+ref.table+` WHERE id = ? AND household_id = ?`,
			ref.id, householdID).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			return ErrNotFound
		}
	}
	if b.CategoryID != 0 {
		var direction Direction
		if err := s.q.QueryRowContext(ctx, `SELECT classification FROM categories WHERE id = ? AND household_id = ?`,
			b.CategoryID, householdID).Scan(&direction); err != nil {
			return err
		}
		if direction != b.Direction {
			return ErrInvalid
		}
	}
	if b.PlanID != 0 {
		plan, err := s.GetBooking(ctx, householdID, b.PlanID)
		if err != nil {
			return err
		}
		if plan.Direction != b.Direction {
			return ErrInvalid
		}
	}
	if b.Decision == "transfer" {
		var own int64
		if err := s.q.QueryRowContext(ctx, `SELECT account_id FROM bank_imports WHERE id = ? AND household_id = ?`,
			b.ImportID, householdID).Scan(&own); err != nil {
			return err
		}
		if b.TransferAccountID == 0 || b.TransferAccountID == own {
			return ErrInvalid
		}
	}
	if b.Decision == "accepted" && (b.CategoryID == 0 || b.TransferAccountID != 0) {
		return ErrInvalid
	}
	return nil
}

func (s *Store) replaceBankAllocation(ctx context.Context, householdID, id int64, b BankTransaction) error {
	if _, err := s.q.ExecContext(ctx, `DELETE FROM bank_splits WHERE transaction_id = ?`, id); err != nil {
		return err
	}
	if _, err := s.q.ExecContext(ctx, `DELETE FROM bank_tags WHERE transaction_id = ?`, id); err != nil {
		return err
	}
	if b.Decision != "accepted" && (b.Decision != "pending" || len(b.Splits) == 0) {
		return nil
	}
	var total int64
	for _, sp := range b.Splits {
		if sp.Cents < 0 || sp.Cents > b.AmountCents {
			return ErrInvalid
		}
		total += sp.Cents
		if total > b.AmountCents {
			return ErrInvalid
		}
		if err := affected(s.q.ExecContext(ctx, `INSERT INTO bank_splits (transaction_id, member_id, cents)
			SELECT ?, id, ? FROM members WHERE id = ? AND household_id = ?`,
			id, sp.Cents, sp.MemberID, householdID)); err != nil {
			return err
		}
	}
	if len(b.Splits) == 0 || total != b.AmountCents {
		return ErrInvalid
	}
	for _, tag := range b.TagIDs {
		if err := affected(s.q.ExecContext(ctx, `INSERT INTO bank_tags (transaction_id, tag_id)
			SELECT ?, id FROM tags WHERE id = ? AND household_id = ?`, id, tag, householdID)); err != nil {
			return err
		}
	}
	return nil
}

// ReviewBankTransaction replaces a review decision atomically. Reopening a row
// removes its accepted allocations; closed months must first be reopened.
func (s *Store) ReviewBankTransaction(ctx context.Context, householdID int64, b BankTransaction, allowDuplicate bool) error {
	if err := ValidateBankTransaction(b); err != nil {
		return err
	}
	return s.withTx(ctx, func(tx *Store) error {
		var previousDate, previousDecision string
		var importID int64
		var original BankTransaction
		err := tx.q.QueryRowContext(ctx, `SELECT booked_on, decision, import_id, direction, amount_cents, name, reference, source_fingerprint FROM bank_transactions
			WHERE id = ? AND household_id = ?`, b.ID, householdID).Scan(&previousDate, &previousDecision, &importID,
			&original.Direction, &original.AmountCents, &original.Name, &original.Reference, &original.SourceFingerprint)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if previousDecision != "pending" && b.Decision != "pending" {
			return ErrInvalid
		}
		b.ImportID = importID
		original.BookedOn = previousDate
		if original.SourceFingerprint == "" {
			original.SourceFingerprint = BankSourceFingerprint(original)
		}
		for _, month := range []string{previousDate[:7], b.BookedOn[:7]} {
			if err := tx.ensureOpenMonth(ctx, householdID, month); err != nil {
				return err
			}
		}
		if err := tx.validateBankReferences(ctx, householdID, b); err != nil {
			return err
		}
		if b.Decision == "accepted" && !allowDuplicate {
			var count int
			if err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM bank_transactions t
				JOIN bank_imports i ON i.id = t.import_id
				WHERE t.household_id = ? AND t.id != ? AND t.decision = 'accepted' AND t.fingerprint = ?
				AND i.account_id = (SELECT account_id FROM bank_imports WHERE id = ?)`,
				householdID, b.ID, PaymentFingerprint(b), importID).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				return ErrDuplicatePayment
			}
		}
		if err := affected(tx.q.ExecContext(ctx, `UPDATE bank_transactions SET
			booked_on = ?, direction = ?, amount_cents = ?, name = ?, reference = ?, fingerprint = ?,
			decision = ?, category_id = `+categoryRef+`,
			plan_id = (SELECT id FROM bookings WHERE id = ? AND household_id = ?),
			transfer_account_id = (SELECT id FROM bank_accounts WHERE id = ? AND household_id = ?),
			cost_nature = ?, budget_class = ?, settle = ?, source_fingerprint = ?,
			analysis = CASE WHEN ? = 'pending' THEN 'null' ELSE analysis END WHERE id = ? AND household_id = ?`,
			b.BookedOn, b.Direction, b.AmountCents, b.Name, b.Reference, PaymentFingerprint(b), b.Decision,
			b.CategoryID, householdID, b.PlanID, householdID, b.TransferAccountID, householdID,
			b.CostNature, b.BudgetClass, b.Settle, original.SourceFingerprint, b.Decision, b.ID, householdID)); err != nil {
			return err
		}
		return tx.replaceBankAllocation(ctx, householdID, b.ID, b)
	})
}

// ReviewBankSuggestion evaluates a proposed acceptance and writes it against
// one snapshot. The callback must be read-only and must not perform network I/O.
func (s *Store) ReviewBankSuggestion(ctx context.Context, householdID int64, prepare func(HouseholdSnapshot) (BankTransaction, error)) error {
	return s.withTx(ctx, func(tx *Store) error {
		snapshot, err := tx.readHousehold(ctx, householdID)
		if err != nil {
			return err
		}
		payment, err := prepare(snapshot)
		if err != nil {
			return err
		}
		if payment.Decision != "accepted" {
			return ErrInvalid
		}
		return tx.ReviewBankTransaction(ctx, householdID, payment, false)
	})
}

// SetMonthReconciled closes only past months without pending review decisions.
func (s *Store) SetMonthReconciled(ctx context.Context, householdID int64, month string, closed bool) error {
	if _, err := time.Parse("2006-01", month); err != nil {
		return ErrInvalid
	}
	return s.withTx(ctx, func(tx *Store) error {
		if !closed {
			_, err := tx.q.ExecContext(ctx, `DELETE FROM reconciled_months WHERE household_id = ? AND month = ?`, householdID, month)
			return err
		}
		if month >= time.Now().Format("2006-01") {
			return ErrInvalid
		}
		var accounts, pending int
		if err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM bank_accounts WHERE household_id = ?`,
			householdID).Scan(&accounts); err != nil {
			return err
		}
		if accounts == 0 {
			return ErrInvalid
		}
		if err := tx.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM bank_transactions
			WHERE household_id = ? AND substr(booked_on,1,7) = ? AND decision = 'pending'`,
			householdID, month).Scan(&pending); err != nil {
			return err
		}
		if pending != 0 {
			return ErrPendingPayments
		}
		_, err := tx.q.ExecContext(ctx, `INSERT INTO reconciled_months (household_id, month) VALUES (?, ?)
			ON CONFLICT DO NOTHING`, householdID, month)
		return err
	})
}

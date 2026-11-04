package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func bankFixture(t *testing.T) (*Store, int64, BankAccount, BankTransaction) {
	t.Helper()
	st, ctx, hh := seededStore(t)
	members, err := st.ListMembers(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) == 0 {
		t.Fatal("missing members")
	}
	if err := st.CreateBankAccount(ctx, hh.ID, BankAccount{Name: "Test account", MemberID: members[0].ID}); err != nil {
		t.Fatal(err)
	}
	l, err := st.BankLedger(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	row := BankTransaction{
		BookedOn: "2020-01-15", Direction: DirExpense, Name: "Test payment", Reference: "Synthetic only",
		AmountCents: 101, Decision: "pending", CategoryID: firstExpenseCategory(ctx, t, st, hh).ID,
		CostNature: CostVariable, BudgetClass: ClassNeed, Settle: true,
	}
	return st, hh.ID, l.Accounts[0], row
}

func TestBankImportReviewClosureAndBackup(t *testing.T) {
	st, hh, account, row := bankFixture(t)
	ctx := t.Context()
	batch := BankImport{AccountID: account.ID, Digest: strings.Repeat("a", 64), Filename: "synthetic.pdf"}
	if err := st.CreateBankImport(ctx, hh, batch, []BankTransaction{row, row}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateBankImport(ctx, hh, batch, []BankTransaction{row}); !errors.Is(err, ErrDuplicateImport) {
		t.Fatalf("duplicate file: %v", err)
	}
	if err := st.SetMonthReconciled(ctx, hh, "2020-01", true); !errors.Is(err, ErrPendingPayments) {
		t.Fatalf("pending closure: %v", err)
	}
	l, _ := st.BankLedger(ctx, hh)
	first, second := l.Transactions[0], l.Transactions[1]
	first.Decision = "accepted"
	first.Splits = []BankSplit{{MemberID: account.MemberID, Cents: 101}}
	if err := st.ReviewBankTransaction(ctx, hh, first, false); err != nil {
		t.Fatal(err)
	}
	if err := st.DiscardBankImport(ctx, hh, first.ImportID); !errors.Is(err, ErrBankHistoryInUse) {
		t.Fatalf("discard accepted history: %v", err)
	}
	if err := st.DeleteBankAccount(ctx, hh, account.ID); !errors.Is(err, ErrBankHistoryInUse) {
		t.Fatalf("delete used account: %v", err)
	}
	if err := st.ReviewBankTransaction(ctx, hh, first, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("replayed decision: %v", err)
	}
	second.Decision, second.Splits = "accepted", first.Splits
	if err := st.ReviewBankTransaction(ctx, hh, second, false); !errors.Is(err, ErrDuplicatePayment) {
		t.Fatalf("duplicate payment: %v", err)
	}
	if err := st.ReviewBankTransaction(ctx, hh, second, true); err != nil {
		t.Fatalf("genuine identical payment: %v", err)
	}
	if err := st.SetMonthReconciled(ctx, hh, "2020-01", true); err != nil {
		t.Fatal(err)
	}
	first.Decision = "pending"
	if err := st.ReviewBankTransaction(ctx, hh, first, false); !errors.Is(err, ErrClosedMonth) {
		t.Fatalf("closed edit: %v", err)
	}
	batch.Digest = strings.Repeat("b", 64)
	if err := st.CreateBankImport(ctx, hh, batch, []BankTransaction{row}); !errors.Is(err, ErrClosedMonth) {
		t.Fatalf("closed import: %v", err)
	}
	if err := st.DeleteMember(ctx, hh, account.MemberID); !errors.Is(err, ErrBankMemberInUse) {
		t.Fatalf("owner delete: %v", err)
	}
	if err := st.DeleteCategory(ctx, hh, row.CategoryID); !errors.Is(err, ErrCategoryInUse) {
		t.Fatalf("category delete: %v", err)
	}
	before, err := st.BankLedger(ctx, hh)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := st.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Import(ctx, backup); err != nil {
		t.Fatalf("restore ledger: %v", err)
	}
	after, err := st.BankLedger(ctx, hh)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("ledger changed during backup round trip")
	}
	if err := st.SetMonthReconciled(ctx, hh, "2020-01", false); err != nil {
		t.Fatal(err)
	}
	if err := st.ReviewBankTransaction(ctx, hh, first, false); err != nil {
		t.Fatal(err)
	}
	if err := st.ResetBookings(ctx, hh); err != nil {
		t.Fatal(err)
	}
	l, _ = st.BankLedger(ctx, hh)
	if len(l.Transactions) != 0 || len(l.Imports) != 0 || len(l.ClosedMonths) != 0 || len(l.Accounts) != 1 {
		t.Fatalf("reset ledger: %+v", l)
	}
	if err := st.DeleteBankAccount(ctx, hh, account.ID); err != nil {
		t.Fatalf("delete unused account: %v", err)
	}
	if err := st.DeleteHousehold(ctx, hh); err != nil {
		t.Fatalf("household cascade: %v", err)
	}
}

func TestBankScopeAndAtomicity(t *testing.T) {
	st, hh, account, row := bankFixture(t)
	ctx := t.Context()
	other, err := st.CreateHouseholdSeeded(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	batch := BankImport{AccountID: account.ID, Digest: strings.Repeat("c", 64), Filename: "test.pdf"}
	if err := st.CreateBankImport(ctx, other.ID, batch, []BankTransaction{row}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign account: %v", err)
	}
	bad := row
	bad.BookedOn = "2020-02-31"
	if err := st.CreateBankImport(ctx, hh, batch, []BankTransaction{row, bad}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid date: %v", err)
	}
	exists, _ := st.HasBankImport(ctx, hh, account.ID, batch.Digest)
	if exists {
		t.Fatal("partial import persisted")
	}
	if err := st.CreateBankImport(ctx, hh, batch, []BankTransaction{row}); err != nil {
		t.Fatal(err)
	}
	l, _ := st.BankLedger(ctx, hh)
	p := l.Transactions[0]
	p.Decision, p.Splits = "accepted", []BankSplit{{MemberID: account.MemberID, Cents: 100}}
	if err := st.ReviewBankTransaction(ctx, hh, p, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbalanced allocation: %v", err)
	}
	p.Splits[0].Cents = 101
	if err := st.ReviewBankTransaction(ctx, other.ID, p, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign payment: %v", err)
	}
	otherMembers, _ := st.ListMembers(ctx, other.ID)
	p.Splits[0].MemberID = otherMembers[0].ID
	if err := st.ReviewBankTransaction(ctx, hh, p, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign allocation: %v", err)
	}
	l, _ = st.BankLedger(ctx, hh)
	if l.Transactions[0].Decision != "pending" || len(l.Transactions[0].Splits) != 0 {
		t.Fatal("failed review was not rolled back")
	}
	if err := st.DiscardBankImport(ctx, other.ID, p.ImportID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign discard: %v", err)
	}
	if err := st.DiscardBankImport(ctx, hh, p.ImportID); err != nil {
		t.Fatalf("discard draft: %v", err)
	}
	exists, _ = st.HasBankImport(ctx, hh, account.ID, batch.Digest)
	if exists {
		t.Fatal("discard did not release document digest")
	}
	if err := st.SetMonthReconciled(ctx, hh, time.Now().Format("2006-01"), true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("current month closed: %v", err)
	}
}

func TestBankTransferAndFuturePlan(t *testing.T) {
	st, hh, account, row := bankFixture(t)
	ctx := t.Context()
	if err := st.CreateBankAccount(ctx, hh, BankAccount{Name: "Savings", MemberID: account.MemberID}); err != nil {
		t.Fatal(err)
	}
	batch := BankImport{AccountID: account.ID, Digest: strings.Repeat("d", 64), Filename: "test.pdf"}
	if err := st.CreateBankImport(ctx, hh, batch, []BankTransaction{row, row}); err != nil {
		t.Fatal(err)
	}
	l, _ := st.BankLedger(ctx, hh)
	p := l.Transactions[0]
	p.Decision, p.TransferAccountID = "transfer", account.ID
	if err := st.ReviewBankTransaction(ctx, hh, p, false); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self transfer: %v", err)
	}
	p.TransferAccountID = l.Accounts[1].ID
	if err := st.ReviewBankTransaction(ctx, hh, p, false); err != nil {
		t.Fatal(err)
	}
	p = l.Transactions[1]
	p.Decision, p.Splits = "accepted", []BankSplit{{MemberID: account.MemberID, Cents: 101}}
	if err := st.ReviewBankTransaction(ctx, hh, p, false); err != nil {
		t.Fatal(err)
	}
	start := time.Now().AddDate(0, 1, 0).Format("2006-01-02")
	if err := st.CreatePlanFromPayment(ctx, hh, p.ID, FreqMonthly, start); err != nil {
		t.Fatal(err)
	}
	if err := st.CreatePlanFromPayment(ctx, hh, p.ID, FreqMonthly, start); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate plan: %v", err)
	}
	l, _ = st.BankLedger(ctx, hh)
	if l.Transactions[0].Decision != "transfer" || l.Transactions[1].PlanID == 0 {
		t.Fatal("decision or plan missing")
	}
}

package store

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestReconciliationPreservesDecisionsCorrectionsAndMultiplicity(t *testing.T) {
	st, household, account, payment := bankFixture(t)
	ctx := t.Context()
	batch := BankImportBatch{Import: BankImport{AccountID: account.ID, Digest: strings.Repeat("a", 64), Filename: "synthetic.csv"}, Payments: []BankTransaction{payment, payment}}
	result, err := st.ReconcileBankImports(ctx, household, []BankImportBatch{batch})
	if err != nil || result.Added != 2 || result.Existing != 0 {
		t.Fatalf("first: %+v %v", result, err)
	}
	ledger, err := st.BankLedger(ctx, household)
	if err != nil {
		t.Fatal(err)
	}
	first, second := ledger.Transactions[0], ledger.Transactions[1]
	first.Decision = "accepted"
	first.Splits = []BankSplit{{MemberID: account.MemberID, Cents: first.AmountCents}}
	if err := st.ReviewBankTransaction(ctx, household, first, false); err != nil {
		t.Fatal(err)
	}
	second.Decision = "ignored"
	if err := st.ReviewBankTransaction(ctx, household, second, false); err != nil {
		t.Fatal(err)
	}
	before, err := st.BankLedger(ctx, household)
	if err != nil {
		t.Fatal(err)
	}
	batch.Import.Digest, batch.Import.Filename = strings.Repeat("b", 64), "renamed.csv"
	result, err = st.ReconcileBankImports(ctx, household, []BankImportBatch{batch})
	if err != nil || result.Added != 0 || result.Existing != 2 {
		t.Fatalf("repeat: %+v %v", result, err)
	}
	after, err := st.BankLedger(ctx, household)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("duplicate-only upload changed existing records or provenance")
	}
	batch.Payments = append(batch.Payments, payment)
	result, err = st.ReconcileBankImports(ctx, household, []BankImportBatch{batch})
	if err != nil || result.Added != 1 || result.Existing != 2 {
		t.Fatalf("additional identical occurrence: %+v %v", result, err)
	}
	first.Decision = "pending"
	if err := st.ReviewBankTransaction(ctx, household, first, false); err != nil {
		t.Fatal(err)
	}
	first.Name, first.Reference, first.BookedOn, first.AmountCents = "Corrected name", "Corrected reference", "2020-02-20", 777
	first.Decision = "accepted"
	first.Splits = []BankSplit{{MemberID: account.MemberID, Cents: 777}}
	if err := st.ReviewBankTransaction(ctx, household, first, false); err != nil {
		t.Fatal(err)
	}
	before, err = st.BankLedger(ctx, household)
	if err != nil {
		t.Fatal(err)
	}
	result, err = st.ReconcileBankImports(ctx, household, []BankImportBatch{batch})
	if err != nil || result.Added != 0 || result.Existing != 3 || result.Preserved != 1 || result.ReviewMonth != "2020-02" {
		t.Fatalf("corrected original recreated: %+v %v", result, err)
	}
	after, err = st.BankLedger(ctx, household)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("reimport overwrote corrected data")
	}
	snapshot, err := st.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	result, err = st.ReconcileBankImports(ctx, household, []BankImportBatch{batch})
	if err != nil || result.Added != 0 || result.Existing != 3 {
		t.Fatalf("backup lost original identities: %+v %v", result, err)
	}
}

func TestReconciliationIsAtomicScopedAndRespectsClosedMonths(t *testing.T) {
	st, household, account, payment := bankFixture(t)
	ctx := t.Context()
	batch := BankImportBatch{Import: BankImport{AccountID: account.ID, Digest: strings.Repeat("a", 64), Filename: "synthetic.csv"}, Payments: []BankTransaction{payment}}
	if _, err := st.ReconcileBankImports(ctx, household, []BankImportBatch{batch}); err != nil {
		t.Fatal(err)
	}
	ledger, err := st.BankLedger(ctx, household)
	if err != nil {
		t.Fatal(err)
	}
	stored := ledger.Transactions[0]
	stored.Decision = "accepted"
	stored.Splits = []BankSplit{{MemberID: account.MemberID, Cents: stored.AmountCents}}
	if err := st.ReviewBankTransaction(ctx, household, stored, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMonthReconciled(ctx, household, "2020-01", true); err != nil {
		t.Fatal(err)
	}
	if result, err := st.ReconcileBankImports(ctx, household, []BankImportBatch{batch}); err != nil || result.Added != 0 || result.Existing != 1 {
		t.Fatalf("unchanged closed month: %+v %v", result, err)
	}
	before, err := st.BankLedger(ctx, household)
	if err != nil {
		t.Fatal(err)
	}
	open, closed := payment, payment
	open.BookedOn, open.Name = "2020-03-10", "New open payment"
	closed.Name = "New closed payment"
	batch.Import.Digest = strings.Repeat("b", 64)
	batch.Payments = []BankTransaction{open, closed}
	if result, err := st.ReconcileBankImports(ctx, household, []BankImportBatch{batch}); !errors.Is(err, ErrClosedMonth) || result != (BankReconciliation{}) {
		t.Fatalf("closed month did not roll back whole upload: %+v %v", result, err)
	}
	after, err := st.BankLedger(ctx, household)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("partial reconciliation persisted")
	}
	other, err := st.CreateHouseholdSeeded(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReconcileBankImports(ctx, other.ID, []BankImportBatch{batch}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign account: %v", err)
	}
	batch.Payments = []BankTransaction{open}
	if _, err := st.ReconcileBankImports(ctx, household, []BankImportBatch{batch, batch}); !errors.Is(err, ErrInvalid) {
		t.Fatal("duplicate destinations accepted")
	}
}

func TestReconciliationUsesCurrentRowsNotAnOldFileDigest(t *testing.T) {
	st, household, account, payment := bankFixture(t)
	ctx := t.Context()
	batch := BankImportBatch{Import: BankImport{AccountID: account.ID, Digest: strings.Repeat("a", 64), Filename: "synthetic.csv"}, Payments: []BankTransaction{payment}}
	var wg sync.WaitGroup
	results := make(chan BankReconciliation, 2)
	failures := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			result, err := st.ReconcileBankImports(ctx, household, []BankImportBatch{batch})
			results <- result
			failures <- err
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	total := BankReconciliation{}
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	for result := range results {
		total.Added += result.Added
		total.Existing += result.Existing
	}
	if total.Added != 1 || total.Existing != 1 {
		t.Fatalf("concurrent uploads duplicated rows: %+v", total)
	}
	ledger, err := st.BankLedger(ctx, household)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.q.ExecContext(ctx, "DELETE FROM bank_transactions WHERE household_id = ? AND id = ?", household, ledger.Transactions[0].ID); err != nil {
		t.Fatal(err)
	}
	result, err := st.ReconcileBankImports(ctx, household, []BankImportBatch{batch})
	if err != nil || result.Added != 1 || result.Existing != 0 {
		t.Fatalf("old digest blocked a missing current row: %+v %v", result, err)
	}
	ledger, err = st.BankLedger(ctx, household)
	if err != nil || len(ledger.Imports) != 1 || len(ledger.Transactions) != 1 {
		t.Fatal("minimal provenance was duplicated")
	}
}

func TestLegacySourceIdentityAndBackupValidation(t *testing.T) {
	st, household, account, payment := bankFixture(t)
	ctx := t.Context()
	if err := st.CreateBankImport(ctx, household, BankImport{AccountID: account.ID, Digest: strings.Repeat("a", 64), Filename: "synthetic.csv"}, []BankTransaction{payment}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Version = 3
	snapshot.Households[0].Bank.Transactions[0].SourceFingerprint = ""
	if err := st.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	ledger, err := st.BankLedger(ctx, household)
	if err != nil {
		t.Fatal(err)
	}
	edited := ledger.Transactions[0]
	edited.Name = "Corrected legacy payment"
	if err := st.ReviewBankTransaction(ctx, household, edited, false); err != nil {
		t.Fatal(err)
	}
	result, err := st.ReconcileBankImports(ctx, household, []BankImportBatch{{Import: BankImport{AccountID: account.ID, Digest: strings.Repeat("b", 64), Filename: "again.csv"}, Payments: []BankTransaction{payment}}})
	if err != nil || result.Added != 0 || result.Existing != 1 || result.Preserved != 1 {
		t.Fatalf("legacy identity not captured before correction: %+v %v", result, err)
	}
	snapshot.Households[0].Bank.Transactions[0].SourceFingerprint = "invalid"
	if err := st.Import(ctx, snapshot); !errors.Is(err, ErrBadSnapshot) {
		t.Fatalf("invalid source identity accepted: %v", err)
	}
}

func TestSourceMatchingUsesOriginalAndCurrentFieldsWithoutLosingOccurrences(t *testing.T) {
	a := BankTransaction{Name: "A", BookedOn: "2020-01-01", Direction: DirExpense, AmountCents: 100}
	b, c := a, a
	b.Name, c.Name = "B", "C"
	first, second := b, c
	first.SourceFingerprint, second.SourceFingerprint = BankSourceFingerprint(a), BankSourceFingerprint(a)
	for _, incoming := range [][]BankTransaction{{a, b}, {b, a}, {a, c}, {b, c}, {a, a}} {
		matches, err := matchBankPayments(t.Context(), incoming, []BankTransaction{first, second})
		if err != nil || len(matches) != 2 {
			t.Fatalf("identity aliases lost a match: %d %v", len(matches), err)
		}
	}
	matches, err := matchBankPayments(t.Context(), []BankTransaction{a, b, c}, []BankTransaction{first, second})
	if err != nil || len(matches) != 2 {
		t.Fatal("one existing row matched more than once")
	}
	left, right := a, a
	left.Name, left.Reference = "A|B", "C"
	right.Name, right.Reference = "A", "B|C"
	if BankSourceFingerprint(left) == BankSourceFingerprint(right) {
		t.Fatal("field separator collision")
	}
}

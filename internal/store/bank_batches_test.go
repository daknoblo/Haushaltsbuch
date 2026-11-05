package store

import (
	"errors"
	"strings"
	"testing"
)

func TestMappedBankImportsAreAtomic(t *testing.T) {
	s, hh, first, payment := bankFixture(t)
	ctx := t.Context()
	if err := s.CreateBankAccount(ctx, hh, BankAccount{MemberID: first.MemberID, Name: "Mapped savings"}); err != nil {
		t.Fatal(err)
	}
	accounts, err := s.ListBankAccounts(ctx, hh)
	if err != nil {
		t.Fatal(err)
	}
	var second BankAccount
	for _, a := range accounts {
		if a.Name == "Mapped savings" {
			second = a
		}
	}
	batches := []BankImportBatch{
		{Import: BankImport{AccountID: first.ID, Digest: strings.Repeat("a", 64), Filename: "synthetic-multi.csv"}, Payments: []BankTransaction{payment}},
		{Import: BankImport{AccountID: second.ID, Digest: strings.Repeat("a", 64), Filename: "synthetic-multi.csv"}, Payments: []BankTransaction{payment}},
	}
	batches[1].Payments[0].BookedOn = "2020-02-01"
	if err := s.SetMonthReconciled(ctx, hh, "2020-02", true); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBankImports(ctx, hh, batches); !errors.Is(err, ErrClosedMonth) {
		t.Fatalf("closed month: %v", err)
	}
	ledger, _ := s.BankLedger(ctx, hh)
	if len(ledger.Imports) != 0 || len(ledger.Transactions) != 0 {
		t.Fatal("failed second group left the first group persisted")
	}
	if err := s.SetMonthReconciled(ctx, hh, "2020-02", false); err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateHouseholdSeeded(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	foreign, _ := s.ListBankAccounts(ctx, other.ID)
	batches[1].Import.AccountID = foreign[0].ID
	if err := s.CreateBankImports(ctx, hh, batches); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign destination: %v", err)
	}
	ledger, _ = s.BankLedger(ctx, hh)
	if len(ledger.Imports) != 0 {
		t.Fatal("foreign destination did not roll back")
	}
	batches[1].Import.AccountID = first.ID
	if err := s.CreateBankImports(ctx, hh, batches); !errors.Is(err, ErrInvalid) {
		t.Fatalf("merged destinations: %v", err)
	}
	batches[1].Import.AccountID = second.ID
	if err := s.CreateBankImport(ctx, hh, batches[1].Import, batches[1].Payments); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBankImports(ctx, hh, batches); !errors.Is(err, ErrDuplicateImport) {
		t.Fatalf("duplicate group: %v", err)
	}
	ledger, _ = s.BankLedger(ctx, hh)
	if len(ledger.Imports) != 1 || ledger.Imports[0].AccountID != second.ID {
		t.Fatal("duplicate group partially committed another group")
	}
	if err := s.ResetBookings(ctx, hh); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateBankImports(ctx, hh, batches); err != nil {
		t.Fatal(err)
	}
	ledger, _ = s.BankLedger(ctx, hh)
	if len(ledger.Imports) != 2 || len(ledger.Transactions) != 2 {
		t.Fatal("not all groups imported")
	}
	snapshot, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Import(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	restored, err := s.BankLedger(ctx, hh)
	if err != nil || len(restored.Imports) != 2 || len(restored.Transactions) != 2 {
		t.Fatal("mapped imports lost in backup round trip")
	}
}

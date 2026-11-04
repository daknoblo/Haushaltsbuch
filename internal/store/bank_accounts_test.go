package store

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func assertDefaultAccounts(t *testing.T, s *Store, householdID int64) []BankAccount {
	t.Helper()
	members, err := s.ListMembers(t.Context(), householdID)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := s.ListBankAccounts(t.Context(), householdID)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members {
		count := 0
		for _, a := range accounts {
			if a.MemberID == m.ID && a.Default {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("member %d has %d default accounts", m.ID, count)
		}
	}
	return accounts
}

func TestMemberBankAccountLifecycle(t *testing.T) {
	s, ctx, hh := seededStore(t)
	initial := assertDefaultAccounts(t, s, hh.ID)
	if len(initial) != 2 {
		t.Fatalf("seeded accounts: %+v", initial)
	}
	m, err := s.CreateMember(ctx, hh.ID, "New member", "#123456")
	if err != nil {
		t.Fatal(err)
	}
	accounts := assertDefaultAccounts(t, s, hh.ID)
	if len(accounts) != 3 {
		t.Fatal("member did not get exactly one account")
	}
	original := accounts[2]
	if original.MemberID != m.ID {
		t.Fatal("wrong owner")
	}
	if err := s.UpdateMember(ctx, hh.ID, m.ID, "Renamed", "#123456"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSeed(ctx); err != nil {
		t.Fatal(err)
	}
	if got := assertDefaultAccounts(t, s, hh.ID); !reflect.DeepEqual(got, accounts) {
		t.Fatal("rename or startup duplicated accounts")
	}
	if err := s.DeleteBankAccount(ctx, hh.ID, original.ID); !errors.Is(err, ErrDefaultBankAccount) {
		t.Fatalf("last default deleted: %v", err)
	}
	if err := s.CreateBankAccount(ctx, hh.ID, BankAccount{MemberID: m.ID, Name: "Savings"}); err != nil {
		t.Fatal(err)
	}
	accounts = assertDefaultAccounts(t, s, hh.ID)
	extra := accounts[len(accounts)-1]
	if err := s.SetDefaultBankAccount(ctx, hh.ID, extra.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameBankAccount(ctx, hh.ID, extra.ID, "Emergency fund"); err != nil {
		t.Fatal(err)
	}
	if err := s.RenameBankAccount(ctx, hh.ID, extra.ID, original.Name); !errors.Is(err, ErrBankAccountName) {
		t.Fatalf("duplicate label: %v", err)
	}
	if err := s.DeleteBankAccount(ctx, hh.ID, original.ID); err != nil {
		t.Fatal(err)
	}
	assertDefaultAccounts(t, s, hh.ID)
	other, err := s.CreateHouseholdSeeded(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDefaultBankAccount(ctx, other.ID, extra.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-household default: %v", err)
	}
	if err := s.RenameBankAccount(ctx, other.ID, extra.ID, "Forged"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-household rename: %v", err)
	}
	if err := s.DeleteMember(ctx, hh.ID, m.ID); err != nil {
		t.Fatalf("unused automatic accounts prevent member deletion: %v", err)
	}
	if got := assertDefaultAccounts(t, s, hh.ID); len(got) != 2 {
		t.Fatal("unused account survived member deletion")
	}
}

func TestCreateMemberAndAccountAreAtomic(t *testing.T) {
	s, ctx, hh := seededStore(t)
	before, err := s.ListMembers(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.q.ExecContext(ctx, `CREATE TRIGGER fail_account BEFORE INSERT ON bank_accounts BEGIN SELECT RAISE(ABORT, 'test rejection'); END;`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMember(ctx, hh.ID, "Must roll back", ""); err == nil {
		t.Fatal("account error ignored")
	}
	after, err := s.ListMembers(ctx, hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("member created without account")
	}
}

func TestDefaultBankAccountMigrationPreservesHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`)
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "0009" {
			break
		}
		content, err := migrationsFS.ReadFile("migrations/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		exec(string(content))
		exec(`INSERT INTO schema_migrations VALUES (?, '')`, entry.Name())
	}
	exec(`INSERT INTO households(id,name,sort_order,created_at) VALUES(1,'Legacy',0,'')`)
	for id := 1; id <= 4; id++ {
		exec(`INSERT INTO members(id,household_id,name,color,sort_order) VALUES(?,1,'Same name','',?)`, id, id)
	}
	exec(`INSERT INTO bank_accounts(id,household_id,member_id,name) VALUES
		(100,1,1,'Existing'),(101,1,1,'Second'),(102,1,4,'Girokonto (2)'),(103,1,4,'Girokonto (2) #1')`)
	exec(`INSERT INTO bank_imports(id,household_id,account_id,digest,filename,created_at) VALUES(50,1,100,?,'legacy.pdf','')`, strings.Repeat("a", 64))
	exec(`INSERT INTO reconciled_months VALUES(1,'2020-01')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	accounts := assertDefaultAccounts(t, st, 1)
	if len(accounts) != 6 {
		t.Fatalf("accounts lost or duplicated: %+v", accounts)
	}
	foundExisting, foundCollision := false, false
	for _, a := range accounts {
		if a.ID == 100 && a.Name == "Existing" && a.Default {
			foundExisting = true
		}
		if a.MemberID == 2 && a.Name == "Girokonto (2) #2" && a.Default {
			foundCollision = true
		}
	}
	if !foundExisting || !foundCollision {
		t.Fatalf("existing/default collision: %+v", accounts)
	}
	ledger, err := st.BankLedger(ctx, 1)
	if err != nil || len(ledger.Imports) != 1 || ledger.Imports[0].AccountID != 100 || len(ledger.ClosedMonths) != 1 {
		t.Fatalf("history changed: %+v %v", ledger, err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if got := assertDefaultAccounts(t, st, 1); !reflect.DeepEqual(got, accounts) {
		t.Fatal("reopening duplicated accounts")
	}
}

func TestBankDefaultsAndGlobalModelBackup(t *testing.T) {
	s, ctx, hh := seededStore(t)
	if err := s.SetAIDeployment(ctx, "saved-model"); err != nil {
		t.Fatal(err)
	}

	other, err := s.CreateHouseholdSeeded(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetActiveHousehold(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	if model, _ := s.AIDeployment(ctx); model != "saved-model" {
		t.Fatal("model is not global")
	}
	snap, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Version != 3 || snap.AIDeployment != "saved-model" {
		t.Fatal("model not backed up")
	}
	if err := s.Import(ctx, snap); err != nil {
		t.Fatal(err)
	}
	got, err := s.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got.CreatedAt = snap.CreatedAt
	if !reflect.DeepEqual(snap, got) {
		t.Fatal("backup round trip changed defaults or configuration")
	}
	if err := s.ResetBookings(ctx, hh.ID); err != nil {
		t.Fatal(err)
	}
	if model, _ := s.AIDeployment(ctx); model != "saved-model" {
		t.Fatal("booking reset cleared global model")
	}
	if err := s.SetAIDeployment(ctx, "https://invalid"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid model: %v", err)
	}
	for _, version := range []int{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			old := Snapshot{Version: version, ActiveID: 1, Households: []HouseholdSnapshot{
				{Household: Household{ID: 1, Name: "First"}, Members: []Member{{ID: 1, Name: "First"}}},
				{Household: Household{ID: 2, Name: "Second"}, Members: []Member{{ID: 2, Name: "Second"}},
					Bank: BankLedger{Accounts: []BankAccount{{ID: 1, MemberID: 2, Name: "Existing"}}}},
			}}
			if err := s.Import(t.Context(), old); err != nil {
				t.Fatal(err)
			}
			first := assertDefaultAccounts(t, s, 1)
			second := assertDefaultAccounts(t, s, 2)
			if len(first) != 1 || first[0].ID == 1 || len(second) != 1 || second[0].ID != 1 {
				t.Fatal("old backup account IDs not preserved")
			}
			if model, _ := s.AIDeployment(t.Context()); model != "" {
				t.Fatal("old backup should require model selection")
			}
		})
	}
}

func TestGlobalImportModelSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureSeed(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetAIDeployment(t.Context(), "persisted-model"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if model, err := s.AIDeployment(t.Context()); err != nil || model != "persisted-model" {
		t.Fatalf("model after restart: %q, %v", model, err)
	}
	if err := s.Reset(t.Context()); err != nil {
		t.Fatal(err)
	}
	if model, _ := s.AIDeployment(t.Context()); model != "" {
		t.Fatal("full reset retained model")
	}
	household, err := s.ActiveHouseholdID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if accounts := assertDefaultAccounts(t, s, household); len(accounts) != 2 {
		t.Fatal("reset missing default accounts")
	}
}

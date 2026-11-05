package bankimport

import (
	"errors"
	"strings"
	"testing"
)

func TestSourceAccountGroupingKeepsBoundaries(t *testing.T) {
	data := []byte("Datum;Name;Betrag;Kontonummer;Kontoname;IBAN\n" +
		"15.01.2020;Recipient;-1,00;TEST0001;Checking;OTHER9999\n" +
		"16.01.2020;Recipient;-2,00;TEST0002;Savings;OTHER9999\n" +
		"17.01.2020;Recipient;-3,00;TEST0001;Renamed checking;OTHER8888\n")
	accounts, err := CSVAccounts(t.Context(), data, TableOptions{})
	if err != nil || len(accounts) != 2 {
		t.Fatalf("grouping: %d %v", len(accounts), err)
	}
	if len(accounts[0].Payments) != 2 || len(accounts[1].Payments) != 1 ||
		accounts[0].Payments[1].AmountCents != 300 || accounts[0].Suffix != "0001" || accounts[1].Suffix != "0002" {
		t.Fatal("source boundaries or ordering lost")
	}
	if accounts[0].Name != "Checking" || strings.Contains(accounts[0].Suffix, "TEST") {
		t.Fatal("wrong source display")
	}
	if rows, err := CSV(t.Context(), data, TableOptions{}); !errors.Is(err, ErrAccountMapping) || rows != nil {
		t.Fatalf("single-account API merged accounts: %v", err)
	}
	withoutOwn := []byte("Datum;Name;Betrag;IBAN\n15.01.2020;First;-1,00;OTHER1111\n16.01.2020;Second;-2,00;OTHER2222\n")
	if accounts, err := CSVAccounts(t.Context(), withoutOwn, TableOptions{}); err != nil || len(accounts) != 1 {
		t.Fatalf("counterparty IBAN was mistaken for own account: %v", err)
	}
}

func TestSourceAccountMappingValidation(t *testing.T) {
	for _, input := range []string{
		"Datum;Name;Betrag;Kontonummer;Kontoname\n15.01.2020;First;-1,00;;Checking\n",
		"Datum;Name;Betrag;Kontonummer\n15.01.2020;First;-1,00\n",
		"Datum;Name;Betrag;Kontoname\n15.01.2020;First;-1,00;\n",
	} {
		if accounts, err := CSVAccounts(t.Context(), []byte(input), TableOptions{}); err == nil || accounts != nil {
			t.Fatal("missing source identity accepted")
		}
	}
	custom := []byte("Datum;Name;Betrag;Origin;Label\n15.01.2020;First;-1,00;TEST1;Checking\n16.01.2020;Second;-2,00;TEST2;Savings\n")
	accounts, err := CSVAccounts(t.Context(), custom, TableOptions{Account: "Origin", AccountName: "Label"})
	if err != nil || len(accounts) != 2 {
		t.Fatalf("custom sources: %v", err)
	}
	nameOnly := []byte("Datum;Name;Betrag;Kontoname\n15.01.2020;First;-1,00;Checking\n16.01.2020;Second;-2,00;Savings\n")
	if accounts, err := CSVAccounts(t.Context(), nameOnly, TableOptions{}); err != nil || len(accounts) != 2 {
		t.Fatalf("name-only sources: %v", err)
	}
}

func TestExcelSourceAccounts(t *testing.T) {
	data := xlsxBytes(t, [][]any{
		{"Datum", "Name", "Betrag", "Kontonummer", "Kontoname"},
		{"15.01.2020", "First", -1, "TEST0001", "Checking"},
		{"16.01.2020", "Second", -2, "TEST0002", "Savings"},
	}, nil)
	accounts, err := XLSXAccounts(t.Context(), data, TableOptions{})
	if err != nil || len(accounts) != 2 || accounts[1].Payments[0].AmountCents != 200 {
		t.Fatalf("Excel source grouping: %v", err)
	}
	if _, err := XLSX(t.Context(), data, TableOptions{}); !errors.Is(err, ErrAccountMapping) {
		t.Fatalf("Excel single-account API merged sources: %v", err)
	}
}

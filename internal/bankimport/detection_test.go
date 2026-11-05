package bankimport

import (
	"errors"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestCSVAutomaticFormatsAndDetectedColumns(t *testing.T) {
	for _, test := range []struct {
		name, data, delimiter, decimal, order string
	}{
		{"German", csvStatement, "semicolon", "comma", "dmy"},
		{"US", "Date,Payee,Amount\n01/15/2020,Example,\"-1,234.56\"\n", "comma", "dot", "mdy"},
		{"tab ISO", "Date\tPayee\tAmount\n2020-01-15\tExample\t-1234.56\n", "tab", "dot", "dmy"},
		{"declared", "sep=;\nDate;Payee;Amount\n2020-01-15;Example;-1234,56\n", "semicolon", "comma", "dmy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := CSVTable(t.Context(), []byte(test.data), TableOptions{Decimal: "auto", DateOrder: "auto"})
			if err != nil {
				t.Fatal(err)
			}
			if got.Options.Delimiter != test.delimiter || got.Options.Decimal != test.decimal || got.Options.DateOrder != test.order ||
				got.Options.Date == "" || got.Options.Amount == "" || got.Options.Name == "" || got.Options.HeaderRow != 1 ||
				got.Accounts[0].Payments[0].AmountCents != 123456 || got.Accounts[0].Payments[0].BookedOn != "2020-01-15" {
				t.Fatalf("wrong detection: %+v", got.Options)
			}
		})
	}
}

func TestAutomaticDatesNeedEvidenceAcrossTheFile(t *testing.T) {
	header := "Date;Payee;Amount\n"
	ambiguous := "01/02/2020;Example;-1.23\n"
	options := TableOptions{Decimal: "auto", DateOrder: "auto"}
	got, err := CSVTable(t.Context(), []byte(header+ambiguous), options)
	var tableErr *TableError
	if !errors.As(err, &tableErr) || tableErr.Kind != "dateOrder" || got.Accounts != nil {
		t.Fatalf("date order guessed: %v", err)
	}
	resolved, err := CSVTable(t.Context(), []byte(header+ambiguous+"13/02/2020;Other;-2.34\n"), options)
	if err != nil || resolved.Options.DateOrder != "dmy" || resolved.Accounts[0].Payments[0].BookedOn != "2020-02-01" {
		t.Fatalf("whole-file date evidence ignored: %v", err)
	}
	options.DateOrder = "mdy"
	resolved, err = CSVTable(t.Context(), []byte(header+ambiguous), options)
	if err != nil || resolved.Accounts[0].Payments[0].BookedOn != "2020-01-02" {
		t.Fatalf("manual correction failed: %v", err)
	}
	options.DateOrder = "auto"
	_, err = CSVTable(t.Context(), []byte(header+"13/02/2020;First;-1.23\n02/13/2020;Second;-2.34\n"), options)
	if !errors.As(err, &tableErr) || tableErr.Kind != "date" {
		t.Fatalf("mixed date orders accepted: %v", err)
	}
	_, err = CSVTable(t.Context(), []byte(header+"01/02/2999;Future;-1.23\n"), options)
	if !errors.As(err, &tableErr) || tableErr.Kind != "dateOrder" {
		t.Fatalf("date inference used transaction acceptance instead of date syntax: %v", err)
	}
}

func TestAutomaticFormatsDoNotGuessUnknownOrMixedColumns(t *testing.T) {
	options := TableOptions{Decimal: "auto", DateOrder: "auto"}
	for _, data := range []string{
		"When;Who;Money\n2020-01-15;Private example;-12.34\n",
		"Date;Payee;Amount\n2020-01-15;Private example;-12.34\n2020-01-16;Other;-12,34\n",
		"Date;Payee;Amount;Amount\n2020-01-15;Private example;12;13\n",
	} {
		got, err := CSVTable(t.Context(), []byte(data), options)
		if err == nil || got.Accounts != nil {
			t.Fatal("unclear table silently accepted")
		}
		if strings.Contains(err.Error(), "Private example") {
			t.Fatal("private row leaked into error")
		}
	}
	options.Date, options.Name, options.Amount = "When", "Who", "Money"
	got, err := CSVTable(t.Context(), []byte("When;Who;Money\n2020-01-15;Example;-12.34\n"), options)
	if err != nil || got.Options.Date != "When" || got.Accounts[0].Payments[0].AmountCents != 1234 {
		t.Fatalf("manual columns with automatic formats: %v", err)
	}
}

func TestAutomaticWorksheetRequiresUniqueStatementHeaders(t *testing.T) {
	addSheet := func(f *excelize.File) {
		t.Helper()
		if _, err := f.NewSheet("Payments"); err != nil {
			t.Fatal(err)
		}
		header, row := []any{"Date", "Payee", "Amount"}, []any{"2020-01-15", "Example", -12.34}
		if err := f.SetSheetRow("Payments", "A1", &header); err != nil {
			t.Fatal(err)
		}
		if err := f.SetSheetRow("Payments", "A2", &row); err != nil {
			t.Fatal(err)
		}
	}
	options := TableOptions{Decimal: "auto", DateOrder: "auto"}
	unique := xlsxBytes(t, [][]any{{"Summary"}, {"Not a statement"}}, addSheet)
	got, err := XLSXTable(t.Context(), unique, options)
	if err != nil || got.Options.Sheet != "Payments" || got.Accounts[0].Payments[0].AmountCents != 1234 {
		t.Fatalf("unique statement: %+v %v", got.Options, err)
	}
	ambiguous := xlsxBytes(t, [][]any{{"Date", "Payee", "Amount"}, {"2020-01-15", "Invalid statement", "bad amount"}}, addSheet)
	if got, err := XLSXTable(t.Context(), ambiguous, options); !errors.Is(err, ErrSheet) || got.Accounts != nil {
		t.Fatal("another statement sheet was silently ignored")
	}
	options.Sheet = "Payments"
	if got, err := XLSXTable(t.Context(), ambiguous, options); err != nil || len(got.Accounts) != 1 {
		t.Fatalf("explicit sheet: %v", err)
	}
	options.Sheet = ""
	duplicateHeaders := xlsxBytes(t, [][]any{{"Date", "Payee", "Amount", "Amount"}, {"2020-01-15", "Example", 12, 13}}, addSheet)
	if _, err := XLSXTable(t.Context(), duplicateHeaders, options); !errors.Is(err, ErrSheet) {
		t.Fatalf("ambiguous statement headers were silently ignored: %v", err)
	}
}

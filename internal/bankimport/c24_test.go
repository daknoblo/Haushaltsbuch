package bankimport

import (
	"bytes"
	"encoding/csv"
	"errors"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestC24CSVWithOmittedUnusedTrailingColumn(t *testing.T) {
	header := strings.Split("Transaktionstyp,Buchungsdatum,Karteneinsatz,Betrag,Zahlungsempfänger,IBAN,BIC,Verwendungszweck,Beschreibung,Kontonummer,Kontoname,Kategorie,Unterkategorie,Bargeldabhebung", ",")
	expense := []string{"Transfer", "15.01.2020", "", "-12,34", "Synthetic recipient", "", "",
		"Synthetic reference", "Synthetic description", "test-account", "Demo account", "Example", "Example detail"}
	income := append([]string(nil), expense...)
	income[1], income[3], income[4] = "16.01.2020", "123,45", "Synthetic payer"
	var data bytes.Buffer
	data.WriteString("\xef\xbb\xbf")
	writer := csv.NewWriter(&data)
	for _, record := range [][]string{header, expense, income, expense} {
		if err := writer.Write(record); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		t.Fatal(err)
	}
	rows, err := CSV(t.Context(), data.Bytes(), TableOptions{})
	if err != nil || len(rows) != 3 {
		t.Fatalf("synthetic C24 import: %d rows, %v", len(rows), err)
	}
	if rows[0].AmountCents != 1234 || rows[0].Direction != store.DirExpense || rows[0].BookedOn != "2020-01-15" ||
		rows[0].Name != "Synthetic recipient" || rows[0].Reference != "Synthetic reference\nSynthetic description" {
		t.Fatalf("incorrect synthetic expense: %+v", rows[0])
	}
	if rows[1].AmountCents != 12345 || rows[1].Direction != store.DirIncome || rows[1].BookedOn != "2020-01-16" {
		t.Fatalf("incorrect synthetic income: %+v", rows[1])
	}
	if store.PaymentFingerprint(rows[0]) != store.PaymentFingerprint(rows[2]) {
		t.Fatal("identical source rows must remain identical review candidates")
	}
}

func TestCSVTrailingColumnsRespectEveryMapping(t *testing.T) {
	data := []byte("Datum;Name;Betrag;Verwendungszweck;Extra\n15.01.2020;Example;-12,34;Reference\n")
	if rows, err := CSV(t.Context(), data, TableOptions{}); err != nil || len(rows) != 1 {
		t.Fatalf("unused trailing column: %v", err)
	}
	for _, options := range []TableOptions{
		{Date: "Extra"},
		{Name: "Extra"},
		{Amount: "Extra"},
		{Reference: "Extra"},
		{Direction: "Extra"},
		{Currency: "Extra"},
	} {
		rows, err := CSV(t.Context(), data, options)
		var tableErr *TableError
		if !errors.As(err, &tableErr) || tableErr.Kind != "row" || tableErr.Row != 2 || rows != nil {
			t.Fatalf("omitted explicitly mapped column was accepted: %+v, %v", options, err)
		}
	}
	for _, invalid := range []string{
		"Datum;Name;Betrag;Verwendungszweck;Extra\n15.01.2020;Example;-12,34\n",
		"Datum;Name;Betrag;Währung;Extra\n15.01.2020;Example;-12,34\n",
		"Datum;Name;Betrag;S/H;Extra\n15.01.2020;Example;12,34\n",
		"Datum;Name;Soll;Haben;Extra\n15.01.2020;Example;12,34\n",
		"Datum;Name;Betrag\n15.01.2020;Example;-12,34;\n",
		"Datum;Name;Betrag;Extra\n15.01.2020;;-12,34\n",
		"Datum;Name;Betrag;Extra\n31.02.2020;Example;-12,34\n",
	} {
		if rows, err := CSV(t.Context(), []byte(invalid), TableOptions{}); err == nil || rows != nil {
			t.Fatalf("malformed synthetic row accepted: %q", invalid)
		}
	}
}

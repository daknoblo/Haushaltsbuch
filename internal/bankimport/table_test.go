package bankimport

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/charmap"
	textunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

const csvStatement = "Buchungstag;Wertstellung;Begünstigter/Zahlungspflichtiger;Verwendungszweck;Betrag;Währung\n" +
	"15.01.2020;16.01.2020;Müller;Test;-1.234,56;EUR\n" +
	"16.01.2020;17.01.2020;Arbeitgeber;Gehalt;2.345,67;EUR\n" +
	"15.01.2020;16.01.2020;Müller;Test;-1.234,56;EUR\n"

func TestCSVEncodingsDelimitersAndExactCents(t *testing.T) {
	cp, err := charmap.Windows1252.NewEncoder().Bytes([]byte(csvStatement))
	if err != nil {
		t.Fatal(err)
	}
	utf16, _, err := transform.Bytes(textunicode.UTF16(textunicode.LittleEndian, textunicode.UseBOM).NewEncoder(), []byte(csvStatement))
	if err != nil {
		t.Fatal(err)
	}
	utf16BE, _, err := transform.Bytes(textunicode.UTF16(textunicode.BigEndian, textunicode.UseBOM).NewEncoder(), []byte(csvStatement))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		data    []byte
		options TableOptions
	}{
		{"utf8", []byte(csvStatement), TableOptions{}},
		{"bom", append([]byte{0xef, 0xbb, 0xbf}, []byte(csvStatement)...), TableOptions{}},
		{"cp1252", cp, TableOptions{}},
		{"utf16", utf16, TableOptions{}},
		{"utf16be", utf16BE, TableOptions{}},
		{"separator directive", []byte("sep=;\n" + csvStatement), TableOptions{}},
		{"preamble", []byte("Export of synthetic account\n\n" + csvStatement), TableOptions{}},
		{"tab", []byte(strings.ReplaceAll(csvStatement, ";", "\t")), TableOptions{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CSV(t.Context(), tc.data, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 3 || got[0].AmountCents != 123456 || got[1].AmountCents != 234567 ||
				got[0].Direction != store.DirExpense || got[1].Direction != store.DirIncome ||
				got[0].BookedOn != "2020-01-15" || got[0].Name != "Müller" ||
				store.PaymentFingerprint(got[0]) != store.PaymentFingerprint(got[2]) {
				t.Fatalf("parsed rows: %+v", got)
			}
		})
	}
	data := "Date,Payee,Reference,Amount,Currency\r\n" +
		"01/15/2020,Example,\"quoted, reference\nwith a newline\",\"-1,234.56\",EUR\r\n"
	got, err := CSV(t.Context(), []byte(data), TableOptions{Decimal: "dot", DateOrder: "mdy"})
	if err != nil || len(got) != 1 || got[0].AmountCents != 123456 || got[0].Reference != "quoted, reference\nwith a newline" {
		t.Fatalf("comma/quotes: %+v %v", got, err)
	}
}

func TestCSVSplitColumnsDirectionAndMapping(t *testing.T) {
	for _, tc := range []struct {
		data      string
		options   TableOptions
		amount    int64
		direction store.Direction
	}{
		{"Datum;Name;Soll;Haben\n15.01.2020;Rent;12,34;\n", TableOptions{}, 1234, store.DirExpense},
		{"Datum;Name;Soll;Haben\n15.01.2020;Salary;;12,34\n", TableOptions{}, 1234, store.DirIncome},
		{"Datum;Name;Betrag;S/H\n15.01.2020;Rent;12,34;S\n", TableOptions{}, 1234, store.DirExpense},
		{"Datum;Name;Betrag;S/H\n15.01.2020;Salary;12,34;H\n", TableOptions{}, 1234, store.DirIncome},
		{"Datum;Verwendungszweck;Betrag\n15.01.2020;Fallback description;-0,01\n", TableOptions{}, 1, store.DirExpense},
		{"Report\nWhen;Who;Money;Purpose\n2020-01-15;Other;-12.34;Example\n",
			TableOptions{Date: "When", Name: "Who", Amount: "Money", Reference: "Purpose", HeaderRow: 2, Decimal: "dot"}, 1234, store.DirExpense},
	} {
		got, err := CSV(t.Context(), []byte(tc.data), tc.options)
		if err != nil || len(got) != 1 || got[0].AmountCents != tc.amount || got[0].Direction != tc.direction {
			t.Fatalf("%q: %+v %v", tc.data, got, err)
		}
	}
	got, err := CSV(t.Context(), []byte("Datum;Name;Buchungstext;Verwendungszweck;Betrag\n15.01.2020;Example;Card;Reference;-1,23\n"), TableOptions{})
	if err != nil || got[0].Reference != "Card\nReference" {
		t.Fatalf("references: %+v %v", got, err)
	}
}

func TestCSVRejectsBadRowsWithoutPartialResults(t *testing.T) {
	for name, data := range map[string]string{
		"invalid date":       strings.Replace(csvStatement, "15.01.2020", "31.02.2020", 1),
		"short year":         strings.Replace(csvStatement, "15.01.2020", "15.01.20", 1),
		"amount precision":   strings.Replace(csvStatement, "-1.234,56", "-1,234", 1),
		"bad grouping":       strings.Replace(csvStatement, "-1.234,56", "-12.34,56", 1),
		"wrong format":       strings.Replace(csvStatement, "-1.234,56", "-1234.56", 1),
		"nonfinite":          strings.Replace(csvStatement, "-1.234,56", "NaN", 1),
		"foreign currency":   strings.Replace(csvStatement, ";EUR", ";USD", 1),
		"missing currency":   strings.Replace(csvStatement, ";EUR", ";", 1),
		"missing amount":     strings.Replace(csvStatement, "-1.234,56", "", 1),
		"missing name":       strings.Replace(csvStatement, "Müller", "", 1),
		"footer":             csvStatement + "Total;;;;123,00;EUR\n",
		"incomplete row":     csvStatement + "15.01.2020;16.01.2020;Name;Reference;1,00\n",
		"both sides":         "Datum;Name;Soll;Haben\n15.01.2020;Other;1,00;2,00\n",
		"direction conflict": "Datum;Name;Betrag;S/H\n15.01.2020;Other;-1,00;H\n",
		"duplicate headers":  "Datum;Name;Betrag;Betrag\n15.01.2020;Other;1,00;2,00\n",
		"value date only":    "Wertstellung;Name;Betrag\n15.01.2020;Other;1,00\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := CSV(t.Context(), []byte(data), TableOptions{})
			var tableErr *TableError
			if !errors.As(err, &tableErr) || got != nil {
				t.Fatalf("partial/invalid result: %+v %v", got, err)
			}
			if strings.Contains(err.Error(), "Müller") {
				t.Fatal("private cell content in error")
			}
		})
	}
}

func TestCSVLimitsAndCancellation(t *testing.T) {
	header := "Datum;Name;Betrag\n"
	row := "15.01.2020;Example;-1,00\n"
	if got, err := CSV(t.Context(), []byte(header+strings.Repeat(row, 1000)), TableOptions{}); err != nil || len(got) != 1000 {
		t.Fatalf("1000 rows: %d %v", len(got), err)
	}
	for _, data := range [][]byte{
		[]byte(header + strings.Repeat(row, 1001)),
		make([]byte, MaxFileBytes+1),
		[]byte(strings.Repeat("x;", 65) + "\n" + row),
		[]byte(header + "15.01.2020;" + strings.Repeat("x", maxCellBytes+1) + ";-1,00\n"),
		[]byte("Datum;Name;Betrag\n15.01.2020;\x00;-1,00\n"),
	} {
		if got, err := CSV(t.Context(), data, TableOptions{}); err == nil || got != nil {
			t.Fatal("oversized/malformed file accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CSV(ctx, []byte(csvStatement), TableOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func xlsxBytes(t *testing.T, values [][]any, edit func(*excelize.File)) []byte {
	t.Helper()
	f := excelize.NewFile()
	defer func() { _ = f.Close() }()
	for row, columns := range values {
		for col, value := range columns {
			cell, err := excelize.CoordinatesToCellName(col+1, row+1)
			if err != nil {
				t.Fatal(err)
			}
			if err := f.SetCellValue("Sheet1", cell, value); err != nil {
				t.Fatal(err)
			}
		}
	}
	if edit != nil {
		edit(f)
	}
	buffer, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestXLSXDatesNumericAmountsAndSheets(t *testing.T) {
	values := [][]any{
		{"Buchungsdatum", "Name", "Betrag", "Währung"},
		{time.Date(2020, 1, 15, 0, 0, 0, 0, time.UTC), "Rent", -1234.56, "EUR"},
		{"16.01.2020", "Salary", 2345.67, "EUR"},
	}
	data := xlsxBytes(t, values, nil)
	got, err := XLSX(t.Context(), data, TableOptions{})
	if err != nil || len(got) != 2 || got[0].BookedOn != "2020-01-15" || got[0].AmountCents != 123456 || got[1].Direction != store.DirIncome {
		t.Fatalf("Excel values: %+v %v", got, err)
	}
	multiple := xlsxBytes(t, values, func(f *excelize.File) {
		if _, err := f.NewSheet("Other"); err != nil {
			t.Fatal(err)
		}
	})
	if _, err := XLSX(t.Context(), multiple, TableOptions{}); !errors.Is(err, ErrSheet) {
		t.Fatalf("ambiguous worksheet: %v", err)
	}
	if got, err := XLSX(t.Context(), multiple, TableOptions{Sheet: "Sheet1"}); err != nil || len(got) != 2 {
		t.Fatalf("selected worksheet: %+v %v", got, err)
	}
	if _, err := XLSX(t.Context(), multiple, TableOptions{Sheet: "missing"}); !errors.Is(err, ErrSheet) {
		t.Fatalf("missing worksheet: %v", err)
	}
	epoch := xlsxBytes(t, [][]any{{"Date", "Payee", "Amount"}, {42383, "Date system", 1}}, func(f *excelize.File) {
		use1904 := true
		if err := f.SetWorkbookProps(&excelize.WorkbookPropsOptions{Date1904: &use1904}); err != nil {
			t.Fatal(err)
		}
	})
	want, err := excelize.ExcelDateToTime(42383, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err = XLSX(t.Context(), epoch, TableOptions{})
	if err != nil || got[0].BookedOn != want.Format("2006-01-02") {
		t.Fatalf("1904 dates: %+v %v", got, err)
	}
}

func TestXLSXRejectsFormulasAndOversizedXML(t *testing.T) {
	for _, edit := range []func(*excelize.File){
		func(f *excelize.File) {
			if err := f.SetCellFormula("Sheet1", "C2", "1+1"); err != nil {
				t.Fatal(err)
			}
		},
		func(f *excelize.File) {
			if err := f.SetCellValue("Sheet1", "A1051", "outside row limit"); err != nil {
				t.Fatal(err)
			}
		},
		func(f *excelize.File) {
			if err := f.SetCellValue("Sheet1", "BM2", "outside column limit"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		data := xlsxBytes(t, [][]any{{"Datum", "Name", "Betrag"}, {"15.01.2020", "Example", 1}}, edit)
		if rows, err := XLSX(t.Context(), data, TableOptions{}); err == nil || rows != nil {
			t.Fatal("formula or oversized worksheet accepted")
		}
	}
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	file, err := writer.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte(strings.Repeat(" ", maxWorkbookBytes+1))); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := validateWorkbook(t.Context(), data.Bytes()); err == nil {
		t.Fatal("compressed oversized workbook accepted")
	}
}

func TestTableMoneyExactAndBounded(t *testing.T) {
	for _, tc := range []struct {
		text    string
		numeric bool
		want    int64
	}{
		{"1.234,56", false, 123456}, {"-0,01", false, -1}, {"10000000000,00", false, 1_000_000_000_000},
		{"1.2345600e3", true, 123456}, {"0.10", true, 10},
	} {
		got, err := parseTableMoney(tableCell{text: tc.text, numeric: tc.numeric}, "comma")
		if err != nil || got != tc.want {
			t.Fatalf("%s: %d %v", tc.text, got, err)
		}
	}
	for _, text := range []string{"NaN", "Inf", "1e999", "1e-99", "1.001", "10000000000.01"} {
		if _, err := parseTableMoney(tableCell{text: text, numeric: true}, "dot"); err == nil {
			t.Fatalf("accepted %q", text)
		}
	}
}

func FuzzCSVDoesNotPanic(f *testing.F) {
	f.Add(csvStatement)
	f.Add("Date,Payee,Amount\n2020-01-15,Example,1.00\n")
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 64<<10 {
			t.Skip()
		}
		rows, err := CSV(t.Context(), []byte(input), TableOptions{})
		if err != nil && rows != nil {
			t.Fatal("partial result on error")
		}
		for _, row := range rows {
			if err := store.ValidateBankTransaction(row); err != nil {
				t.Fatalf("invalid result: %v", err)
			}
		}
	})
}

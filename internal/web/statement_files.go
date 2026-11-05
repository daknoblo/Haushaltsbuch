package web

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
)

func (s *Server) extractStatement(ctx context.Context, r *http.Request, filename string, content []byte) (bankimport.TableResult, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".pdf":
		if s.bankAI == nil {
			return bankimport.TableResult{}, bankimport.ErrConfiguration
		}
		deployment, err := s.store.AIDeployment(ctx)
		if err != nil {
			return bankimport.TableResult{}, err
		}
		if deployment == "" {
			return bankimport.TableResult{}, bankimport.ErrConfiguration
		}
		text, err := bankimport.PDFText(ctx, content)
		if err != nil {
			return bankimport.TableResult{}, err
		}
		payments, err := s.bankAI.Extract(ctx, deployment, text)
		if err != nil {
			return bankimport.TableResult{}, err
		}
		return bankimport.TableResult{Accounts: []bankimport.SourceAccount{{Payments: payments}}}, nil
	case ".csv", ".xlsx":
		options, err := statementTableOptions(r)
		if err != nil {
			return bankimport.TableResult{}, err
		}
		if strings.EqualFold(filepath.Ext(filename), ".csv") {
			return bankimport.CSVTable(ctx, content, options)
		}
		return bankimport.XLSXTable(ctx, content, options)
	default:
		return bankimport.TableResult{}, bankimport.ErrFile
	}
}

func statementTableOptions(r *http.Request) (bankimport.TableOptions, error) {
	o := bankimport.TableOptions{
		Delimiter: r.FormValue("table_delimiter"), Encoding: r.FormValue("table_encoding"),
		Decimal: r.FormValue("table_decimal"), DateOrder: r.FormValue("table_date_order"),
		Sheet: strings.TrimSpace(r.FormValue("table_sheet")),
		Date:  r.FormValue("column_date"), Amount: r.FormValue("column_amount"), Name: r.FormValue("column_name"),
		Reference: r.FormValue("column_reference"), Debit: r.FormValue("column_debit"), Credit: r.FormValue("column_credit"),
		Direction: r.FormValue("column_direction"), Currency: r.FormValue("column_currency"),
		Account: r.FormValue("column_account"), AccountName: r.FormValue("column_account_name"),
	}
	if value := r.FormValue("table_header"); value != "" {
		number, err := strconv.Atoi(value)
		if err != nil || number < 1 || number > 50 {
			return o, &bankimport.TableError{Kind: "options"}
		}
		o.HeaderRow = number
	}
	if o.Decimal == "" {
		o.Decimal = "auto"
	}
	if o.DateOrder == "" {
		o.DateOrder = "auto"
	}
	return o, nil
}

func tableErrorKey(kind string) string {
	switch kind {
	case "mapping":
		return "import.tableMappingError"
	case "date":
		return "import.tableDateError"
	case "dateOrder":
		return "import.tableAmbiguousDate"
	case "numberFormat":
		return "import.tableAmbiguousNumber"
	case "amount":
		return "import.tableAmountError"
	case "currency":
		return "import.tableCurrencyError"
	case "encoding":
		return "import.tableEncodingError"
	case "formula":
		return "import.tableFormulaError"
	case "limit":
		return "import.tableLimitError"
	case "empty":
		return "import.tableEmptyError"
	case "options":
		return "import.tableOptionsError"
	case "account":
		return "import.sourceAccountError"
	default:
		return "import.tableReadError"
	}

}

func detectedTable(ctx context.Context, result bankimport.TableResult) []string {
	o := result.Options
	if o.Date == "" {
		return nil
	}
	var out []string
	if o.Sheet != "" {
		out = append(out, T(ctx, "import.sheet")+": "+o.Sheet)
	} else {
		delimiter := T(ctx, "import.tab")
		switch o.Delimiter {
		case "semicolon":
			delimiter = T(ctx, "import.semicolon")
		case "comma":
			delimiter = T(ctx, "import.comma")
		}
		encoding := "UTF-8"
		switch o.Encoding {
		case "windows1252":
			encoding = "Windows-1252"
		case "utf16":
			encoding = "UTF-16"
		}
		out = append(out, T(ctx, "import.delimiter")+": "+delimiter, T(ctx, "import.encoding")+": "+encoding)
	}
	decimal, order := T(ctx, "import.decimalComma"), T(ctx, "import.dateDMY")
	if o.Decimal == "dot" {
		decimal = T(ctx, "import.decimalDot")
	}
	if o.DateOrder == "mdy" {
		order = T(ctx, "import.dateMDY")
	}
	out = append(out, T(ctx, "import.decimal")+": "+decimal, T(ctx, "import.dateOrder")+": "+order,
		T(ctx, "import.headerRow")+": "+strconv.Itoa(o.HeaderRow))
	for _, column := range []struct{ label, heading string }{
		{"import.date", o.Date}, {"import.columnAmount", o.Amount}, {"import.debit", o.Debit}, {"import.credit", o.Credit},
		{"import.payee", o.Name}, {"import.reference", strings.Join(result.References, ", ")},
		{"import.directionColumn", o.Direction}, {"import.currency", o.Currency},
		{"import.sourceColumn", o.Account}, {"import.sourceNameColumn", o.AccountName},
	} {
		if column.heading != "" {
			out = append(out, T(ctx, column.label)+": "+cleanName(column.heading))
		}
	}
	return out
}

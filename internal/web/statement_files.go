package web

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
)

func (s *Server) extractStatement(ctx context.Context, r *http.Request, filename string, content []byte) ([]bankimport.SourceAccount, error) {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".pdf":
		if s.bankAI == nil {
			return nil, bankimport.ErrConfiguration
		}
		deployment, err := s.store.AIDeployment(ctx)
		if err != nil {
			return nil, err
		}
		if deployment == "" {
			return nil, bankimport.ErrConfiguration
		}
		text, err := bankimport.PDFText(ctx, content)
		if err != nil {
			return nil, err
		}
		payments, err := s.bankAI.Extract(ctx, deployment, text)
		if err != nil {
			return nil, err
		}
		return []bankimport.SourceAccount{{Payments: payments}}, nil
	case ".csv", ".xlsx":
		options, err := statementTableOptions(r)
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(filepath.Ext(filename), ".csv") {
			return bankimport.CSVAccounts(ctx, content, options)
		}
		return bankimport.XLSXAccounts(ctx, content, options)
	default:
		return nil, bankimport.ErrFile
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
	return o, nil
}

func tableErrorKey(kind string) string {
	switch kind {
	case "mapping":
		return "import.tableMappingError"
	case "date":
		return "import.tableDateError"
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

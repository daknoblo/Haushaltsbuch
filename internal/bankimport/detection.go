package bankimport

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// TableResult includes the effective mapping without exposing payment values.
type TableResult struct {
	Accounts   []SourceAccount
	Options    TableOptions
	References []string
}

// A recognizable but ambiguous statement must not disappear behind another
// worksheet whose columns happen to map cleanly.
func possibleStatement(t table, o TableOptions) bool {
	for i, row := range t.rows {
		if o.HeaderRow != 0 && row.number != o.HeaderRow {
			continue
		}
		if o.HeaderRow == 0 && i >= 50 {
			break
		}
		present := func(explicit string, aliases []string) bool {
			for _, cell := range row.cells {
				if explicit != "" {
					if strings.EqualFold(strings.TrimSpace(cell.text), strings.TrimSpace(explicit)) {
						return true
					}
					continue
				}
				for _, alias := range aliases {
					if headerKey(cell.text) == headerKey(alias) {
						return true
					}
				}
			}
			return false
		}
		if present(o.Date, dateHeadings) && (present(o.Name, nameHeadings) || present(o.Reference, referenceHeadings)) &&
			(present(o.Amount, amountHeadings) || (present(o.Debit, debitHeadings) && present(o.Credit, creditHeadings))) {
			return true
		}
	}
	return false
}

func emptyTableRow(row tableRow) bool {
	for _, cell := range row.cells {
		if strings.TrimSpace(cell.text) != "" || cell.formula {
			return false
		}
	}
	return true
}

func tableCellAt(row tableRow, index int) tableCell {
	if index < 0 || index >= len(row.cells) {
		return tableCell{}
	}
	return row.cells[index]
}

func detectTable(ctx context.Context, t table, o TableOptions) (TableResult, error) {
	header, c, err := locateHeader(t, o)
	if err != nil {
		return TableResult{}, err
	}
	for _, field := range []struct {
		option          *string
		choices         []string
		kind, ambiguous string
		parse           func(tableRow, string) (string, error)
	}{
		{&o.Decimal, []string{"comma", "dot"}, "amount", "numberFormat", func(row tableRow, format string) (string, error) {
			amount, direction, err := tableAmount(tableCellAt(row, c.amount), tableCellAt(row, c.debit), tableCellAt(row, c.credit), tableCellAt(row, c.direction), c, format)
			return fmt.Sprintf("%d:%s", amount, direction), err
		}},
		{&o.DateOrder, []string{"dmy", "mdy"}, "date", "dateOrder", func(row tableRow, format string) (string, error) {
			return tableDate(tableCellAt(row, c.date), format, t.date1904)
		}},
	} {
		if *field.option != "auto" {
			continue
		}
		var values []string
		chosen := ""
		failedRow := 0
		for _, candidate := range field.choices {
			parsed := make([]string, 0, len(t.rows)-header-1)
			valid := true
			for _, row := range t.rows[header+1:] {
				if err := ctx.Err(); err != nil {
					return TableResult{}, err
				}
				if emptyTableRow(row) {
					continue
				}
				value, err := field.parse(row, candidate)
				if err != nil {
					valid = false
					if failedRow == 0 {
						failedRow = row.number
					}
					break
				}
				parsed = append(parsed, value)
			}
			if !valid {
				continue
			}
			if chosen != "" && !slices.Equal(values, parsed) {
				return TableResult{}, tableError(field.ambiguous, 0)
			}
			if chosen == "" {
				chosen, values = candidate, parsed
			}
		}
		if chosen == "" {
			return TableResult{}, tableError(field.kind, failedRow)
		}
		*field.option = chosen
	}
	accounts, err := parseAccounts(ctx, t, o)
	if err != nil {
		return TableResult{}, err
	}
	name := func(index int) string { return tableCellAt(t.rows[header], index).text }
	o.HeaderRow = t.rows[header].number
	o.Date, o.Amount, o.Debit, o.Credit = name(c.date), name(c.amount), name(c.debit), name(c.credit)
	o.Name, o.Direction, o.Currency = name(c.name), name(c.direction), name(c.currency)
	o.Account, o.AccountName = name(c.account), name(c.accountName)
	result := TableResult{Accounts: accounts, Options: o}
	for _, index := range c.references {
		result.References = append(result.References, name(index))
	}
	return result, nil
}

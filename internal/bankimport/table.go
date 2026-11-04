package bankimport

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// Structured imports share the existing statement limits and never call AI.
const (
	MaxFileBytes  = 10 << 20
	maxTableRows  = 1050
	maxColumns    = 64
	maxCellBytes  = 16 << 10
	maxTableBytes = 2 << 20
)

// TableOptions permits explicit correction of bank-specific headings and formats.
type TableOptions struct {
	Delimiter                                                         string
	Encoding                                                          string
	Decimal                                                           string
	DateOrder                                                         string
	Sheet                                                             string
	HeaderRow                                                         int
	Date, Amount, Debit, Credit, Name, Reference, Direction, Currency string
}

// TableError identifies a bad row without reflecting private cell contents.
type TableError struct {
	Kind string
	Row  int
}

func (e *TableError) Error() string {
	return fmt.Sprintf("bank import: table %s at row %d", e.Kind, e.Row)
}

// File-format errors are safe to log; they contain no uploaded data.
var (
	ErrFile  = errors.New("bank import: unsupported or oversized statement file")
	ErrSheet = errors.New("bank import: select an existing worksheet")
)

type tableCell struct {
	text    string
	numeric bool
	formula bool
}

type tableRow struct {
	number int
	cells  []tableCell
}

type table struct {
	rows        []tableRow
	date1904    bool
	strictWidth bool
}

type columns struct {
	date, amount, debit, credit, name, direction, currency int
	references                                             []int
}

func tableError(kind string, row int) error { return &TableError{Kind: kind, Row: row} }

func (o TableOptions) normalized() (TableOptions, error) {
	if o.Decimal == "" {
		o.Decimal = "comma"
	}
	if o.DateOrder == "" {
		o.DateOrder = "dmy"
	}
	if o.Delimiter == "" {
		o.Delimiter = "auto"
	}
	if o.Encoding == "" {
		o.Encoding = "auto"
	}
	if (o.Decimal != "comma" && o.Decimal != "dot") || (o.DateOrder != "dmy" && o.DateOrder != "mdy") ||
		(o.Delimiter != "auto" && o.Delimiter != "comma" && o.Delimiter != "semicolon" && o.Delimiter != "tab") ||
		(o.Encoding != "auto" && o.Encoding != "utf8" && o.Encoding != "windows1252") ||
		o.HeaderRow < 0 || o.HeaderRow > 50 || utf8.RuneCountInString(o.Sheet) > 31 {
		return o, tableError("options", 0)
	}
	for _, name := range []string{o.Date, o.Amount, o.Debit, o.Credit, o.Name, o.Reference, o.Direction, o.Currency} {
		if len(name) > 256 {
			return o, tableError("options", 0)
		}
	}
	return o, nil
}

func headerKey(s string) string {
	s = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss").Replace(strings.ToLower(strings.TrimSpace(s)))
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

func findColumn(row tableRow, explicit string, aliases ...string) (int, error) {
	match := -1
	for i, cell := range row.cells {
		matches := false
		if explicit != "" {
			matches = strings.EqualFold(strings.TrimSpace(cell.text), strings.TrimSpace(explicit))
		} else {
			key := headerKey(cell.text)
			for _, alias := range aliases {
				if key == headerKey(alias) {
					matches = true
					break
				}
			}
		}
		if !matches {
			continue
		}
		if match != -1 {
			return -1, tableError("mapping", row.number)
		}
		match = i
	}
	if explicit != "" && match < 0 {
		return -1, tableError("mapping", row.number)
	}
	return match, nil
}

func mapColumns(row tableRow, o TableOptions) (columns, error) {
	c := columns{date: -1, amount: -1, debit: -1, credit: -1, name: -1, direction: -1, currency: -1}
	fields := []struct {
		target   *int
		explicit string
		aliases  []string
	}{
		{&c.date, o.Date, []string{"Buchungstag", "Buchungsdatum", "Booking date", "Transaction date", "Date", "Datum"}},
		{&c.name, o.Name, []string{"Name", "Payee", "Counterparty", "Beneficiary", "Name Zahlungsbeteiligter", "Begünstigter/Zahlungspflichtiger",
			"Zahlungsempfänger", "Empfänger", "Auftraggeber", "Empfänger/Auftraggeber", "Auftraggeber/Empfänger"}},
		{&c.direction, o.Direction, []string{"Soll/Haben", "S/H", "Debit/Credit", "Direction", "Richtung"}},
		{&c.currency, o.Currency, []string{"Währung", "Currency", "Currency code", "Waehrung"}},
	}
	for _, field := range fields {
		index, err := findColumn(row, field.explicit, field.aliases...)
		if err != nil {
			return c, err
		}
		*field.target = index
	}
	var err error
	if o.Debit == "" && o.Credit == "" {
		c.amount, err = findColumn(row, o.Amount, "Betrag", "Amount", "Umsatz", "Betrag EUR", "Betrag in EUR", "Amount EUR", "Umsatz EUR")
		if err != nil {
			return c, err
		}
	}
	if o.Amount == "" {
		c.debit, err = findColumn(row, o.Debit, "Soll", "Belastung", "Debit", "Debit amount", "Ausgaben")
		if err != nil {
			return c, err
		}
		c.credit, err = findColumn(row, o.Credit, "Haben", "Gutschrift", "Credit", "Credit amount", "Einnahmen")
		if err != nil {
			return c, err
		}
	}
	if o.Reference != "" {
		index, err := findColumn(row, o.Reference)
		if err != nil {
			return c, err
		}
		c.references = []int{index}
	} else {
		for i, cell := range row.cells {
			for _, alias := range []string{"Verwendungszweck", "Buchungstext", "Referenz", "Reference", "Purpose", "Description", "Beschreibung", "Details", "Zahlungsreferenz"} {
				if headerKey(cell.text) == headerKey(alias) {
					c.references = append(c.references, i)
					break
				}
			}
		}
	}
	split := c.debit >= 0 && c.credit >= 0
	if c.date < 0 || (c.name < 0 && len(c.references) == 0) ||
		(c.amount < 0 && !split) || (c.amount >= 0 && (c.debit >= 0 || c.credit >= 0)) ||
		(o.Amount != "" && (o.Debit != "" || o.Credit != "")) {
		return c, tableError("mapping", row.number)
	}
	used := make(map[int]bool)
	for _, index := range []int{c.date, c.amount, c.debit, c.credit, c.name, c.direction, c.currency} {
		if index < 0 {
			continue
		}
		if used[index] || row.cells[index].formula {
			return c, tableError("mapping", row.number)
		}
		used[index] = true
	}
	return c, nil
}

func locateHeader(t table, o TableOptions) (int, columns, error) {
	for i, row := range t.rows {
		if o.HeaderRow != 0 && row.number != o.HeaderRow {
			continue
		}
		if o.HeaderRow == 0 && i >= 50 {
			break
		}
		c, err := mapColumns(row, o)
		if err == nil {
			return i, c, nil
		}
		if o.HeaderRow != 0 {
			return 0, c, err
		}
	}
	return 0, columns{}, tableError("mapping", 0)
}

func parseTable(ctx context.Context, t table, o TableOptions) ([]store.BankTransaction, error) {
	header, c, err := locateHeader(t, o)
	if err != nil {
		return nil, err
	}
	var out []store.BankTransaction
	for _, row := range t.rows[header+1:] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		empty := true
		for _, cell := range row.cells {
			if strings.TrimSpace(cell.text) != "" || cell.formula {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		if len(out) >= 1000 {
			return nil, tableError("limit", row.number)
		}
		if len(row.cells) > len(t.rows[header].cells) ||
			(t.strictWidth && len(row.cells) != len(t.rows[header].cells)) {
			return nil, tableError("row", row.number)
		}
		p, err := parseTableRow(row, c, o, t.date1904)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, tableError("empty", 0)
	}
	return out, nil
}

func parseTableRow(row tableRow, c columns, o TableOptions, date1904 bool) (store.BankTransaction, error) {
	cell := func(index int) tableCell {
		if index < 0 || index >= len(row.cells) {
			return tableCell{}
		}
		return row.cells[index]
	}
	for _, index := range append([]int{c.date, c.amount, c.debit, c.credit, c.name, c.direction, c.currency}, c.references...) {
		if cell(index).formula {
			return store.BankTransaction{}, tableError("formula", row.number)
		}
	}
	currency := strings.ToUpper(strings.TrimSpace(cell(c.currency).text))
	if c.currency >= 0 && currency != "EUR" && currency != "€" {
		return store.BankTransaction{}, tableError("currency", row.number)
	}
	date, err := tableDate(cell(c.date), o.DateOrder, date1904)
	if err != nil {
		return store.BankTransaction{}, tableError("date", row.number)
	}
	amount, direction, err := tableAmount(cell(c.amount), cell(c.debit), cell(c.credit), cell(c.direction), c, o.Decimal)
	if err != nil {
		return store.BankTransaction{}, tableError("amount", row.number)
	}
	references := make([]string, 0, len(c.references))
	for _, index := range c.references {
		if v := strings.TrimSpace(cell(index).text); v != "" {
			references = append(references, v)
		}
	}
	reference := strings.Join(references, "\n")
	name := strings.TrimSpace(cell(c.name).text)
	if c.name < 0 && reference != "" {
		runes := []rune(reference)
		name = string(runes[:min(120, len(runes))])
	}
	p := store.BankTransaction{
		BookedOn: date, Direction: direction, AmountCents: amount, Name: name, Reference: reference,
		Decision: "pending", CostNature: store.CostVariable, BudgetClass: store.ClassNeed, Settle: true,
	}
	if err := store.ValidateBankTransaction(p); err != nil {
		return store.BankTransaction{}, tableError("row", row.number)
	}
	return p, nil
}

func tableAmount(amount, debit, credit, direction tableCell, c columns, decimal string) (int64, store.Direction, error) {
	dir := store.DirIncome
	var cents int64
	if c.amount >= 0 {
		var err error
		cents, err = parseTableMoney(amount, decimal)
		if err != nil {
			return 0, "", err
		}
		if cents < 0 {
			dir = store.DirExpense
			cents = -cents
		}
	} else {
		d, c := strings.TrimSpace(debit.text), strings.TrimSpace(credit.text)
		var spent, received int64
		var err error
		if d != "" {
			spent, err = parseTableMoney(debit, decimal)
			if err != nil {
				return 0, "", err
			}
		}
		if c != "" {
			received, err = parseTableMoney(credit, decimal)
			if err != nil {
				return 0, "", err
			}
		}
		if spent < 0 || received < 0 || (d == "" && c == "") || (spent > 0 && received > 0) ||
			(spent == 0 && received == 0 && d != "" && c != "") {
			return 0, "", ErrResult
		}
		if spent > 0 || (d != "" && c == "") {
			cents, dir = spent, store.DirExpense
		} else {
			cents = received
		}
	}
	if c.direction >= 0 {
		var explicit store.Direction
		switch headerKey(direction.text) {
		case "s", "soll", "d", "debit", "expense", "ausgabe", "ausgaben", "belastung":
			explicit = store.DirExpense
		case "h", "haben", "c", "credit", "income", "einnahme", "einnahmen", "gutschrift":
			explicit = store.DirIncome
		default:
			return 0, "", ErrResult
		}
		if (dir == store.DirExpense && explicit == store.DirIncome) || (c.amount < 0 && dir != explicit) {
			return 0, "", ErrResult
		}
		dir = explicit
	}
	return cents, dir, nil
}

var plainDecimal = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]{1,2})?$`)

func parseTableMoney(cell tableCell, decimal string) (int64, error) {
	s := strings.TrimSpace(cell.text)
	if len(s) == 0 || len(s) > 80 {
		return 0, ErrResult
	}
	if !cell.numeric {
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(s, "EUR"), "€"))
		sign := ""
		if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "+") {
			sign, s = s[:1], s[1:]
		}
		sep, group := ",", "."
		if decimal == "dot" {
			sep, group = ".", ","
		}
		parts := strings.Split(s, sep)
		if len(parts) > 2 || (len(parts) == 2 && (len(parts[1]) < 1 || len(parts[1]) > 2)) {
			return 0, ErrResult
		}
		groups := strings.Split(parts[0], group)
		for i, g := range groups {
			if g == "" || (len(groups) > 1 && ((i == 0 && len(g) > 3) || (i > 0 && len(g) != 3))) || !digits(g) {
				return 0, ErrResult
			}
		}
		s = sign + strings.Join(groups, "")
		if len(parts) == 2 {
			if !digits(parts[1]) {
				return 0, ErrResult
			}
			s += "." + parts[1]
		}
	}
	if !plainDecimal.MatchString(s) {
		return 0, ErrResult
	}
	if at := strings.IndexAny(s, "eE"); at >= 0 {
		exp, err := strconv.Atoi(s[at+1:])
		if err != nil || exp < -12 || exp > 12 {
			return 0, ErrResult
		}
	}
	value, ok := new(big.Rat).SetString(s)
	if !ok {
		return 0, ErrResult
	}
	value.Mul(value, big.NewRat(100, 1))
	if !value.IsInt() || !value.Num().IsInt64() {
		return 0, ErrResult
	}
	cents := value.Num().Int64()
	if cents < -1_000_000_000_000 || cents > 1_000_000_000_000 {
		return 0, ErrResult
	}
	return cents, nil
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func tableDate(cell tableCell, order string, date1904 bool) (string, error) {
	if cell.numeric {
		return excelDate(cell.text, date1904)
	}
	s := strings.TrimSpace(cell.text)
	layouts := make([]string, 0, 8)
	if order == "mdy" {
		layouts = append(layouts, "2006-01-02", "01/02/2006", "1/2/2006")
	} else {
		layouts = append(layouts, "2006-01-02", "02.01.2006", "2.1.2006", "02/01/2006", "2/1/2006")
	}
	layouts = append(layouts, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05")
	for _, layout := range layouts {
		if value, err := time.Parse(layout, s); err == nil {
			return value.Format("2006-01-02"), nil
		}
	}
	return "", ErrResult
}

package web

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"golang.org/x/net/html"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func importColorsFixture() (calc.Data, store.BankLedger) {
	data := calc.Data{
		Members: []store.Member{{ID: 11, Name: "Alex", Color: "#2563eb"}, {ID: 12, Name: "Sam", Color: "#db2777"}},
		Categories: []store.Category{
			{ID: 1, Name: "Actual category", Classification: store.DirExpense, Color: "#f97316"},
			{ID: 2, Name: "Plan category", Classification: store.DirExpense, Color: "#8b5cf6"},
		},
		Tags: []store.Tag{{ID: 21, Name: "Shared tag", Color: "#14b8a6"}},
		Bookings: []store.Booking{{ID: 31, Name: "Synthetic subscription", CategoryID: 2, Direction: store.DirExpense,
			AmountCents: 1399, Frequency: store.FreqMonthly, Interval: 1, StartsOn: "2020-01-01", EndsOn: "2020-12-31", SplitMode: store.SplitFixed}},
		Splits:   map[int64][]store.BookingSplit{31: {{MemberID: 11, Value: 500}, {MemberID: 12, Value: 899}}},
		TagLinks: map[int64][]int64{31: {21}},
	}
	payment := store.BankTransaction{ID: 41, ImportID: 51, BookedOn: "2020-01-15", Name: "Synthetic subscription",
		Direction: store.DirExpense, AmountCents: 1399, Decision: "pending", CategoryID: 1, PlanID: 31,
		CostNature: store.CostFix, BudgetClass: store.ClassWant, Settle: true,
		Splits: []store.BankSplit{{MemberID: 11, Cents: 500}, {MemberID: 12, Cents: 899}}, TagIDs: []int64{21}}
	payment.Fingerprint = store.PaymentFingerprint(payment)
	ledger := store.BankLedger{
		Accounts:     []store.BankAccount{{ID: 61, MemberID: 11, Name: "Synthetic account"}},
		Imports:      []store.BankImport{{ID: 51, AccountID: 61, Filename: "synthetic.csv"}},
		Transactions: []store.BankTransaction{payment},
	}
	return data, ledger
}

func assertImportBadgeColors(t *testing.T, data calc.Data, ledger store.BankLedger) ImportRow {
	t.Helper()
	rows := importRows(t.Context(), ledger, data, "2020-01")
	if len(rows) != 1 {
		t.Fatal("expected one payment")
	}
	row := rows[0]
	if row.CategoryColor != data.Categories[0].Color || row.PlanColor != data.Categories[1].Color {
		t.Fatal("payment and linked plan must resolve their own category colors")
	}
	var body bytes.Buffer
	if err := importPayment(Nav{Month: "2020-01"}, ImportVM{Data: data, Ledger: ledger}, row).Render(t.Context(), &body); err != nil {
		t.Fatal(err)
	}
	document, err := html.Parse(&body)
	if err != nil {
		t.Fatal(err)
	}
	var text func(*html.Node) string
	text = func(node *html.Node) string {
		if node.Type == html.TextNode {
			return node.Data
		}
		var out strings.Builder
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			out.WriteString(text(child))
		}
		return out.String()
	}
	expected := map[string]string{
		"Actual category": tagStyle(data.Categories[0].Color),
		Tf(t.Context(), "import.planLabel", "Synthetic subscription"): tagStyle(data.Categories[1].Color),
		"Alex: " + FormatEUR(500):                                     tagStyle(data.Members[0].Color),
		"Sam: " + FormatEUR(899):                                      tagStyle(data.Members[1].Color),
		"Shared tag":                                                  tagStyle(data.Tags[0].Color),
	}
	found := map[string]bool{}
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.Data == "span" || node.Data == "a") {
			if want, ok := expected[text(node)]; ok {
				attrs := map[string]string{}
				for _, attr := range node.Attr {
					attrs[attr.Key] = attr.Val
				}
				if strings.TrimSuffix(attrs["style"], ";") != want || !slices.Contains(strings.Fields(attrs["class"]), "border") {
					t.Errorf("%s: style %q, class %q; want %q and border", text(node), attrs["style"], attrs["class"], want)
				}
				found[text(node)] = true
			}
			if text(node) == ReviewStageLabel(t.Context(), row.Stage) {
				for _, attr := range node.Attr {
					if attr.Key == "style" {
						t.Error("review state inherited an entity color")
					}
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if len(found) != len(expected) {
		t.Fatalf("colored labels missing: got %v, want %v", found, expected)
	}
	return row
}

func TestImportLabelsUseCurrentSettingsColors(t *testing.T) {
	for _, decision := range []string{"pending", "accepted"} {
		t.Run(decision, func(t *testing.T) {
			data, ledger := importColorsFixture()
			ledger.Transactions[0].Decision = decision
			before := assertImportBadgeColors(t, data, ledger)
			data.Categories[0].Color, data.Categories[1].Color = "#16a34a", "#eab308"
			data.Members[0].Color, data.Members[1].Color, data.Tags[0].Color = "#0891b2", "#9333ea", "#dc2626"
			after := assertImportBadgeColors(t, data, ledger)
			if before.AllocationLabel != after.AllocationLabel || before.CanQuick != after.CanQuick ||
				before.Stage != after.Stage || before.Payment.AmountCents != after.Payment.AmountCents {
				t.Fatal("changing colors altered review behavior or monetary labels")
			}
		})
	}
}

func TestImportColorsUseExistingSafeFallback(t *testing.T) {
	data, ledger := importColorsFixture()
	data.Categories[0].Color = "red;display:none"
	data.Categories[1].Color = ""
	data.Members[0].Color = "url(https://invalid.example)"
	data.Tags[0].Color = `"><script>`
	assertImportBadgeColors(t, data, ledger)
}

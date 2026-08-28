package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// The plausibility suite is what the unit tests cannot be: it asks whether the
// packages still agree with each other. Every check below is a promise the app
// makes to whoever reads a screen — that a total is the sum of its parts, that
// two views of the same money say the same thing, that a settlement squares.
//
// It runs against the real store, the real handlers and the real API, on a book
// that exercises every feature once. A release that breaks one of these has
// broken something a reader would notice.

// plausibilityBook fills a household with one of everything: both directions,
// every rhythm, every way of splitting, an override, a booking nobody carries
// and one kept out of the settlement.
func plausibilityBook(t *testing.T, srv *Server, h store.Household) map[string]int64 {
	t.Helper()
	ctx := t.Context()

	cats, err := srv.store.ListCategories(ctx, h.ID)
	if err != nil {
		t.Fatalf("categories: %v", err)
	}
	cat := func(name string) int64 {
		for _, c := range cats {
			if c.Name == name {
				return c.ID
			}
		}
		t.Fatalf("the seed has no category %q", name)
		return 0
	}
	members, err := srv.store.ListMembers(ctx, h.ID)
	if err != nil || len(members) < 2 {
		t.Fatalf("members: %v (%d)", err, len(members))
	}
	a, b := members[0].ID, members[1].ID

	ids := map[string]int64{}
	add := func(key string, bk store.Booking, splits []store.SplitInput) {
		t.Helper()
		bk.HouseholdID = h.ID
		bk.Name = key
		created, err := srv.store.CreateBooking(ctx, bk, splits, nil)
		if err != nil {
			t.Fatalf("create %s: %v", key, err)
		}
		ids[key] = created.ID
	}

	year := func(b store.Booking) store.Booking {
		b.StartsOn, b.EndsOn = "2026-01-01", "2026-12-31"
		return b
	}
	expense := func(category string, amount int64, freq store.Frequency, nature store.CostNature, class store.BudgetClass) store.Booking {
		return year(store.Booking{
			CategoryID: cat(category), Direction: store.DirExpense, AmountCents: amount,
			Frequency: freq, Interval: 1, DuePoint: store.DueStart,
			CostNature: nature, BudgetClass: class, SplitMode: store.SplitEqual,
			Settle: true, PayerMemberID: &a,
		})
	}

	// Income: one that recurs and one that happens once.
	add("Gehalt", year(store.Booking{
		CategoryID: cat("Gehalt"), Direction: store.DirIncome, AmountCents: 300000,
		Frequency: store.FreqMonthly, Interval: 1, DuePoint: store.DueStart,
		SplitMode: store.SplitEqual, PayerMemberID: &a,
	}), []store.SplitInput{{MemberID: a}})
	add("Bonus", store.Booking{
		HouseholdID: h.ID, CategoryID: cat("Sonstige Einnahmen"), Direction: store.DirIncome,
		AmountCents: 120000, Frequency: store.FreqOnce, Interval: 1, DuePoint: store.DueStart,
		StartsOn: "2026-04-10", SplitMode: store.SplitEqual, PayerMemberID: &a,
	}, []store.SplitInput{{MemberID: a}})

	// Expenses across every rhythm, nature and class. Versicherung, Nebenkosten
	// and Sonstiges all come to a hundred euro a month, and the two subscriptions
	// tie inside their category: rows that cost the same are where an unstable
	// sort shows itself.
	add("Miete", expense("Miete", 100000, store.FreqMonthly, store.CostFix, store.ClassNeed),
		[]store.SplitInput{{MemberID: a}, {MemberID: b}})
	add("Versicherung", expense("Versicherung", 120000, store.FreqYearly, store.CostFix, store.ClassNeed),
		[]store.SplitInput{{MemberID: a}})
	add("Abschlag", expense("Nebenkosten", 30000, store.FreqQuarterly, store.CostFix, store.ClassNeed),
		[]store.SplitInput{{MemberID: a}, {MemberID: b}})
	add("Haushaltsgeld", expense("Lebensmittel", 5000, store.FreqWeekly, store.CostVariable, store.ClassNeed),
		[]store.SplitInput{{MemberID: a}})
	add("Streaming", expense("Abo", 1500, store.FreqMonthly, store.CostFix, store.ClassWant),
		[]store.SplitInput{{MemberID: a}})
	add("Zeitung", expense("Abo", 1500, store.FreqMonthly, store.CostFix, store.ClassWant),
		[]store.SplitInput{{MemberID: a}})

	// The three ways of dividing a booking.
	percent := expense("Freizeit", 50000, store.FreqMonthly, store.CostVariable, store.ClassWant)
	percent.SplitMode = store.SplitPercent
	add("Freizeit", percent, []store.SplitInput{{MemberID: a, Value: 60}, {MemberID: b, Value: 40}})

	fixed := expense("Mobilität", 80000, store.FreqMonthly, store.CostVariable, store.ClassWant)
	fixed.SplitMode = store.SplitFixed
	add("Einkauf", fixed, []store.SplitInput{{MemberID: a, Value: 50000}, {MemberID: b, Value: 30000}})

	// A savings rate everyone runs for themselves stays out of the settlement.
	unsettled := expense("Sparrate", 40000, store.FreqMonthly, store.CostFix, store.ClassSaving)
	unsettled.Settle = false
	add("Sparrate", unsettled, []store.SplitInput{{MemberID: a}, {MemberID: b}})

	// A booking nobody carries: it costs the household but settles nothing.
	add("Herrenlos", expense("Sonstiges", 10000, store.FreqMonthly, store.CostVariable, store.ClassWant), nil)

	// An amount that differs for part of the year.
	over := expense("Strom", 20000, store.FreqMonthly, store.CostFix, store.ClassNeed)
	over.PayerMemberID = &b
	add("Strom", over, []store.SplitInput{{MemberID: a}, {MemberID: b}})
	if _, err := srv.store.CreateOverride(ctx, h.ID, store.BookingOverride{
		BookingID: ids["Strom"], StartsOn: "2026-01-01", EndsOn: "2026-03-31", AmountCents: 9000,
	}); err != nil {
		t.Fatalf("override: %v", err)
	}
	return ids
}

func plausibilityFixture(t *testing.T) (*Server, http.Handler, store.Household, calc.Data) {
	t.Helper()
	srv, h, hh := newTestServer(t)
	plausibilityBook(t, srv, hh)
	data, err := srv.loadHouseholdData(t.Context(), hh.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return srv, h, hh, data
}

func plausibilityMonths() []string {
	out := make([]string, 12)
	for i := range out {
		out[i] = fmt.Sprintf("2026-%02d", i+1)
	}
	return out
}

// A report has to add up on its own before anything else can be believed.
func TestPlausibilityAMonthAddsUp(t *testing.T) {
	_, _, _, data := plausibilityFixture(t)

	for _, month := range plausibilityMonths() {
		rep := calc.PeriodReport(data, []string{month}, calc.Everyone)

		if got := rep.IncomeCents - rep.ExpenseCents; got != rep.BalanceCents {
			t.Errorf("%s: balance = %d, want income less expenses %d", month, rep.BalanceCents, got)
		}
		if got := rep.FixedCents() + rep.VariableCents(); got != rep.ExpenseCents {
			t.Errorf("%s: fixed plus variable = %d, want expenses %d", month, got, rep.ExpenseCents)
		}
		var byClass int64
		for _, v := range rep.ByBudgetClass {
			byClass += v
		}
		if byClass != rep.ExpenseCents {
			t.Errorf("%s: the 50/30/20 classes hold %d, expenses are %d", month, byClass, rep.ExpenseCents)
		}
		var byCategory int64
		for _, c := range rep.Categories {
			byCategory += c.Cents
		}
		if byCategory != rep.ExpenseCents {
			t.Errorf("%s: the categories hold %d, expenses are %d", month, byCategory, rep.ExpenseCents)
		}
	}
}

// What the household spends is what its people carry plus what nobody claimed.
// A figure that only added up in one of the two scopes would send the settlement
// after money that was never there.
func TestPlausibilityTheScopesAgree(t *testing.T) {
	_, _, _, data := plausibilityFixture(t)

	for _, month := range plausibilityMonths() {
		months := []string{month}
		household := calc.PeriodReport(data, months, calc.Everyone)

		var carried int64
		for _, m := range data.Members {
			carried += calc.PeriodReport(data, months, m.ID).ExpenseCents
		}
		// "Herrenlos" is the only booking without a carrier and it recurs
		// monthly at ten euro.
		if want := household.ExpenseCents - 10000; carried != want {
			t.Errorf("%s: the members carry %d, the household spends %d less the uncarried %d",
				month, carried, household.ExpenseCents, want)
		}
	}
}

// Every average divides by the whole range, so a year is the twelve months
// added up and shared out again. Anything else and switching the period would
// quietly change what the figures mean.
func TestPlausibilityAYearIsItsMonths(t *testing.T) {
	_, _, _, data := plausibilityFixture(t)
	months := plausibilityMonths()

	var income, expense int64
	for _, m := range months {
		rep := calc.PeriodReport(data, []string{m}, calc.Everyone)
		income += rep.IncomeCents
		expense += rep.ExpenseCents
	}

	year := calc.PeriodReport(data, months, calc.Everyone)
	// Rounding happens per month on both paths, so one cent per month is the
	// most the two can drift apart.
	if diff := year.IncomeCents - income/12; diff > 12 || diff < -12 {
		t.Errorf("year income = %d, the months average to %d", year.IncomeCents, income/12)
	}
	if diff := year.ExpenseCents - expense/12; diff > 12 || diff < -12 {
		t.Errorf("year expenses = %d, the months average to %d", year.ExpenseCents, expense/12)
	}
}

// The year table is a second view of the same book and has to reconcile with
// the reports, column by column and row by row.
func TestPlausibilityTheYearTableReconciles(t *testing.T) {
	_, _, _, data := plausibilityFixture(t)
	months := plausibilityMonths()
	m := calc.BuildMatrix(data, months, calc.Everyone)

	income := m.Band(calc.BandIncome).Total
	fixed := m.Band(calc.BandFixed).Total
	variable := m.Band(calc.BandVariable).Total

	for i, month := range months {
		rep := calc.PeriodReport(data, []string{month}, calc.Everyone)
		if income.Cents[i] != rep.IncomeCents {
			t.Errorf("%s: table income %d, report %d", month, income.Cents[i], rep.IncomeCents)
		}
		if got := fixed.Cents[i] + variable.Cents[i]; got != rep.ExpenseCents {
			t.Errorf("%s: table expenses %d, report %d", month, got, rep.ExpenseCents)
		}
		if m.Expense.Cents[i] != rep.ExpenseCents {
			t.Errorf("%s: expense row %d, report %d", month, m.Expense.Cents[i], rep.ExpenseCents)
		}
		if want := income.Cents[i] - m.Expense.Cents[i]; m.Surplus.Cents[i] != want {
			t.Errorf("%s: surplus %d, want %d", month, m.Surplus.Cents[i], want)
		}
	}

	if got, want := m.Expense.MeanCents, m.Expense.TotalCents/int64(m.Expense.ActiveMonths); got != want {
		t.Errorf("mean %d is not the total %d over the %d months it ran",
			got, m.Expense.TotalCents, m.Expense.ActiveMonths)
	}

	// A category that unfolds has to equal the bookings underneath it, or the
	// table lies about where the money went.
	for _, band := range []string{calc.BandFixed, calc.BandVariable} {
		for _, row := range m.Band(band).Rows {
			if len(row.Children) == 0 {
				continue
			}
			var sum int64
			for _, child := range row.Children {
				sum += child.TotalCents
			}
			if sum != row.TotalCents {
				t.Errorf("%q holds %d, its bookings add to %d", row.Label, row.TotalCents, sum)
			}
		}
	}
}

// Money that changes hands has to come from somewhere and land somewhere.
func TestPlausibilityTheSettlementSquares(t *testing.T) {
	_, _, _, data := plausibilityFixture(t)
	rep := calc.Settlement(data, plausibilityMonths())

	var net int64
	for _, p := range rep.Positions {
		net += p.NetCents
		if got := p.PaidCents - p.OwedCents; got != p.NetCents {
			t.Errorf("%s: net %d, want paid less owed %d", p.Member.Name, p.NetCents, got)
		}
	}
	if net != 0 {
		t.Errorf("the positions net to %d, want zero", net)
	}

	// NetCents is positive when a member is owed, so the transfer that squares
	// them has to arrive with the same sign.
	moved := make(map[int64]int64)
	for _, tr := range rep.Transfers {
		moved[tr.From.ID] -= tr.Cents
		moved[tr.To.ID] += tr.Cents
	}
	for _, p := range rep.Positions {
		if moved[p.Member.ID] != p.NetCents {
			t.Errorf("%s is owed %d but the transfers move %d",
				p.Member.Name, p.NetCents, moved[p.Member.ID])
		}
	}

	for _, l := range rep.Lines {
		if l.Booking.Name == "Sparrate" {
			t.Error("a booking kept out of the settlement turned up in it")
		}
		if l.Booking.Name == "Herrenlos" {
			t.Error("a booking nobody carries turned up in the settlement")
		}
	}
}

// A ribbon that arrives has to leave again, or the diagram invents money.
func TestPlausibilityTheFlowBalances(t *testing.T) {
	_, _, _, data := plausibilityFixture(t)
	months := plausibilityMonths()
	rep := calc.PeriodReport(data, months, calc.Everyone)
	s := calc.BuildSankey(t.Context(), data, rep, months, 900, 460)

	in := make(map[string]int64)
	out := make(map[string]int64)
	for _, l := range s.Links {
		out[l.Source] += l.Cents
		in[l.Target] += l.Cents
		if l.Cents <= 0 {
			t.Errorf("link %s->%s carries %d", l.Source, l.Target, l.Cents)
		}
	}
	if in["trunk"] != out["trunk"] {
		t.Errorf("the trunk takes %d and gives %d", in["trunk"], out["trunk"])
	}
	for _, n := range s.Nodes {
		if in[n.ID] != 0 && out[n.ID] != 0 && in[n.ID] != out[n.ID] {
			t.Errorf("node %s takes %d and gives %d", n.ID, in[n.ID], out[n.ID])
		}
	}
}

// The ring divides the income it is drawn from, and the rule's three targets
// are the whole of it.
func TestPlausibilityTheRuleRingIsWhole(t *testing.T) {
	_, _, _, data := plausibilityFixture(t)
	rep := calc.PeriodReport(data, plausibilityMonths(), calc.Everyone)

	targets := rep.TargetCents(store.ClassNeed) + rep.TargetCents(store.ClassWant) +
		rep.TargetCents(store.ClassSaving)
	if diff := targets - rep.IncomeCents; diff > 2 || diff < -2 {
		t.Errorf("the three targets add to %d, income is %d", targets, rep.IncomeCents)
	}

	ring := calc.BuildRuleRing(rep)
	var drawn int64
	for _, a := range ring.Arcs {
		drawn += a.Cents
	}
	if want := max(rep.IncomeCents, rep.ExpenseCents); drawn != want {
		t.Errorf("the ring draws %d, want %d", drawn, want)
	}
}

// Every page has to render, and none of them may leak a formatting mishap or an
// untranslated key onto the screen.
func TestPlausibilityEveryPageRenders(t *testing.T) {
	_, h, _, _ := plausibilityFixture(t)

	paths := []string{
		"/", "/bookings", "/bookings?m=2026-04", "/settings",
		"/dashboard", "/dashboard?p=1m", "/dashboard?p=3m", "/dashboard?p=6m",
		"/dashboard?m=2026-04&view=1", "/dashboard?g=class",
	}
	for _, path := range paths {
		w := get(t, h, path)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, w.Code)
			continue
		}
		body := w.Body.String()
		for _, bad := range []string{"%!", "!(EXTRA", "<no value>"} {
			if strings.Contains(body, bad) {
				t.Errorf("GET %s renders %q, which is a formatting mishap", path, bad)
			}
		}
		// An untranslated key falls through as its own name.
		for _, key := range []string{"dash.", "matrix.", "settings.", "bookings."} {
			if strings.Contains(body, ">"+key) {
				t.Errorf("GET %s shows the raw key prefix %q", path, key)
			}
		}
	}
}

// The exports are the same figures on paper. A broken one is only noticed when
// someone needs it, which is the wrong moment.
func TestPlausibilityTheExportsAreProducible(t *testing.T) {
	_, h, _, _ := plausibilityFixture(t)

	for _, path := range []string{
		"/export/overview.pdf?m=2026-04",
		"/export/expenses.pdf?m=2026-04",
		"/export/statistics.pdf?m=2026-04",
		"/export/year.pdf?m=2026-04",
	} {
		w := get(t, h, path)
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, w.Code)
			continue
		}
		if !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF")) {
			t.Errorf("GET %s did not answer with a PDF", path)
		}
	}
}

// The API and the page are two doors to one book. A caller that automates
// against figures the screen contradicts has no way of noticing.
func TestPlausibilityTheAPIAgreesWithTheReport(t *testing.T) {
	_, h, _, data := plausibilityFixture(t)

	for _, month := range plausibilityMonths() {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/report?month="+month, nil)
		r.Header.Set("Authorization", "Bearer "+testAPIToken)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("report %s = %d: %s", month, w.Code, w.Body)
		}

		var got struct {
			IncomeCents  int64 `json:"income_cents"`
			ExpenseCents int64 `json:"expense_cents"`
			BalanceCents int64 `json:"balance_cents"`
			FixedCents   int64 `json:"fixed_cents"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode %s: %v", month, err)
		}
		want := calc.PeriodReport(data, []string{month}, calc.Everyone)
		if got.IncomeCents != want.IncomeCents || got.ExpenseCents != want.ExpenseCents ||
			got.BalanceCents != want.BalanceCents || got.FixedCents != want.FixedCents() {
			t.Errorf("%s: the API says %+v, the report says %d/%d/%d/%d",
				month, got, want.IncomeCents, want.ExpenseCents, want.BalanceCents, want.FixedCents())
		}
	}
}

// The same book has to render the same page twice. Rows are collected out of
// maps, whose order Go deliberately shuffles, so anything that only sorts by
// amount lets two equal lines swap places between one reload and the next.
func TestPlausibilityTheSamePageRendersTheSame(t *testing.T) {
	_, h, _, _ := plausibilityFixture(t)

	for _, path := range []string{
		"/", "/bookings?m=2026-04", "/settings",
		"/dashboard?m=2026-04", "/dashboard?m=2026-04&p=1m", "/dashboard?m=2026-04&g=class",
	} {
		first := get(t, h, path).Body.String()
		for i := range 15 {
			if again := get(t, h, path).Body.String(); again != first {
				t.Errorf("GET %s reads differently on call %d without anything having changed", path, i+2)
				break
			}
		}
	}
}

// A backup is only worth keeping if it comes back identical. This is the one
// check whose failure costs a household its book.
func TestPlausibilityABackupSurvivesTheRoundTrip(t *testing.T) {
	srv, h, hh, _ := plausibilityFixture(t)
	ctx := t.Context()

	before := get(t, h, "/dashboard?m=2026-04").Body.String()
	snapshot, err := srv.store.Export(ctx)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := srv.store.Reset(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := srv.store.Import(ctx, snapshot); err != nil {
		t.Fatalf("import: %v", err)
	}

	if after := get(t, h, "/dashboard?m=2026-04").Body.String(); after != before {
		t.Error("the dashboard reads differently after a backup came back")
	}
	stats, err := srv.store.Stats(ctx, hh.ID)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Bookings != 13 {
		t.Errorf("%d bookings after the round trip, want the 13 that went in", stats.Bookings)
	}
}

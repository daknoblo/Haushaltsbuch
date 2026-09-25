package web

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestElapsedYearMonths(t *testing.T) {
	for _, tc := range []struct {
		anchor, current string
		count           int
		last            string
	}{
		{"2026-12", "2026-09", 9, "2026-09"},
		{"2026-01", "2026-09", 9, "2026-09"},
		{"2025-06", "2026-09", 12, "2025-12"},
		{"2027-01", "2026-09", 0, ""},
		{"2026-01", "2026-01", 1, "2026-01"},
		{"2026-12", "2026-12", 12, "2026-12"},
	} {
		got := elapsedYearMonths(tc.anchor, tc.current)
		if len(got) != tc.count {
			t.Fatalf("%s / %s = %v", tc.anchor, tc.current, got)
		}
		if len(got) > 0 && (got[0] != tc.anchor[:4]+"-01" || got[len(got)-1] != tc.last) {
			t.Errorf("incorrect boundaries: %v", got)
		}
	}
}

func TestDashboardSettlementUsesElapsedYearTotals(t *testing.T) {
	srv, _, hh, data := plausibilityFixture(t)
	anchor := NormalizeMonth("")
	for _, b := range data.Bookings {
		b.StartsOn, b.EndsOn = "", ""
		if b.Frequency == store.FreqOnce {
			b.StartsOn = anchor + "-01"
		}
		splits := make([]store.SplitInput, 0, len(data.Splits[b.ID]))
		for _, split := range data.Splits[b.ID] {
			splits = append(splits, store.SplitInput{MemberID: split.MemberID, Value: split.Value})
		}
		if err := srv.store.SaveBooking(t.Context(), b, splits, data.TagLinks[b.ID]); err != nil {
			t.Fatal(err)
		}
	}
	data, err := srv.loadHouseholdData(t.Context(), hh.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, period := range []string{periodYear, "1m", periodQuarter} {
		vm, err := srv.buildDashboardVM(t.Context(), hh.ID, anchor, period, calc.Everyone, "")
		if err != nil {
			t.Fatal(err)
		}
		months := rangeMonths(period, anchor)
		want := calc.Settlement(data, months)
		wantCosts := calc.Costs(data, months)
		if period == periodYear {
			want = calc.SettlementTotal(data, elapsedYearMonths(anchor, anchor))
			wantCosts = calc.CostsTotal(data, elapsedYearMonths(anchor, anchor))
		}
		if !reflect.DeepEqual(vm.Settlement, want) {
			t.Errorf("%s: settlement is not in the expected unit", period)
		}
		if !reflect.DeepEqual(vm.Costs, wantCosts) {
			t.Errorf("%s: cost overview does not use the settlement period and unit", period)
		}
		recorded := calc.MonthsWithIncome(data, months, calc.Everyone)
		if !reflect.DeepEqual(vm.Report, calc.PeriodReport(data, recorded, calc.Everyone)) {
			t.Errorf("%s: dashboard figures must use months with recorded income", period)
		}

	}
}

func TestDashboardCostsIncludePersonalAndExcludedExpenses(t *testing.T) {
	srv, _, hh, data := plausibilityFixture(t)
	for _, member := range []int64{calc.Everyone, data.Members[0].ID, data.Members[1].ID} {
		vm, err := srv.buildDashboardVM(t.Context(), hh.ID, "2026-05", "1m", member, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(vm.Costs.InvalidBookings) != 1 || vm.Costs.InvalidBookings[0].Name != "Herrenlos" {
			t.Fatalf("unassigned expense must be explicitly reported: %+v", vm.Costs.InvalidBookings)
		}
		costs := vm.Carried()
		if member == data.Members[0].ID && costs.SoleCents != 34667 {
			t.Errorf("own costs = %d, want insurance, subscriptions and groceries (34667)", costs.SoleCents)
		}
		if member == data.Members[1].ID && costs.SoleCents != 0 {
			t.Error("another person's sole costs leaked into the selected view")
		}
		if vm.VisibleCosts(member, false) != costs.SharedCents || vm.VisibleCosts(member, true) != costs.TotalCents() {
			t.Error("visible footer must follow the checkbox without affecting tiles")
		}
		if member != calc.Everyone && (len(vm.CostMembers()) != 1 || vm.CostMembers()[0].ID != member) {
			t.Error("cost tiles/columns do not follow selected person")
		}
		if member == calc.Everyone && len(vm.CostMembers()) != 2 {
			t.Error("household must include every person's cost tile")
		}
		if !reflect.DeepEqual(vm.Settlement, calc.Settlement(data, []string{"2026-05"})) {
			t.Error("cost view changed reimbursement amounts")
		}
		var rendered bytes.Buffer
		if err := settlementCard(vm).Render(t.Context(), &rendered); err != nil {
			t.Fatal(err)
		}
		body := rendered.String()
		for _, marker := range []string{"data-cost-tiles", "data-cost-overview", "data-own-costs", `data-cost-total="false"`, `data-cost-total="true" hidden`, "data-cost-invalid"} {
			if !strings.Contains(body, marker) {
				t.Errorf("missing integrated cost UI marker %q", marker)
			}
		}
		if strings.Contains(body, `data-own-costs checked`) {
			t.Error("own costs must be hidden by default")
		}
		if member != data.Members[1].ID && !strings.Contains(body, `data-own-cost="true" hidden`) {
			t.Error("personal rows are not initially hidden")
		}
	}
}

func TestPersonalCostsAvailableWithoutIncomeOrOtherMembers(t *testing.T) {
	vm := DashboardVM{
		ViewMember: 1,
		Settlement: calc.SettlementReport{Positions: []calc.MemberPosition{{Member: store.Member{ID: 1, Name: "Anna"}}}},
		Costs: calc.CostReport{Lines: []calc.ShareLine{{
			Booking: store.Booking{ID: 1, Name: "Policy", CostNature: store.CostFix},
			Cents:   5000, Shares: map[int64]int64{1: 5000}, Carriers: 1, WithoutSettlement: true,
		}}},
	}
	if !vm.ShowSettlement() || vm.HasSharedCosts() || vm.Carried().FixedCents() != 5000 {
		t.Fatal("a sole member must have access to personal costs independently of income")
	}
	var rendered bytes.Buffer
	if err := settlementCard(vm).Render(t.Context(), &rendered); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.String(), `data-cost-table hidden`) ||
		!strings.Contains(rendered.String(), `data-own-cost="true" hidden`) {
		t.Error("personal-only table must begin hidden, ready for the checkbox")
	}
}
func TestSettlementRendersFullAmountAndInvalidWarning(t *testing.T) {
	srv, h, _, data := plausibilityFixture(t)
	body := get(t, h, "/dashboard?m=2026-05&p=1m").Body.String()
	if !strings.Contains(body, "Gesamtbetrag") || !strings.Contains(body, "Monatsdurchschnitt der Planung") {
		t.Error("missing full amount column or period basis")
	}
	for _, b := range data.Bookings {
		if b.Name != "Freizeit" {
			continue
		}
		if err := srv.store.SaveBooking(t.Context(), b, []store.SplitInput{
			{MemberID: data.Members[0].ID, Value: 30},
			{MemberID: data.Members[1].ID, Value: 20},
		}, data.TagLinks[b.ID]); err != nil {
			t.Fatal(err)
		}
	}
	body = get(t, h, "/dashboard?m=2026-05&p=1m").Body.String()
	if !strings.Contains(body, `role="alert"`) || !strings.Contains(body, "keine Überweisungen") ||
		strings.Contains(body, "Alles ausgeglichen.") || strings.Contains(body, `class="settle-result"`) {
		t.Error("invalid settlement did not block transfer suggestions")
	}
}

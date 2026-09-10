package web

import (
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
		if period == periodYear {
			want = calc.SettlementTotal(data, elapsedYearMonths(anchor, anchor))
		}
		if !reflect.DeepEqual(vm.Settlement, want) {
			t.Errorf("%s: settlement is not in the expected unit", period)
		}
		if !reflect.DeepEqual(vm.Report, calc.PeriodReport(data, months, calc.Everyone)) {
			t.Errorf("%s: unrelated dashboard figures changed", period)
		}
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

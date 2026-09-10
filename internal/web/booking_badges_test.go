package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

func TestBookingSettlementColumnAndWarning(t *testing.T) {
	for _, tc := range []struct {
		name      string
		direction store.Direction
		shared    bool
		settle    bool
		warning   bool
		excluded  bool
	}{
		{"shared excluded", store.DirExpense, true, false, true, true},
		{"shared enabled", store.DirExpense, true, true, false, false},
		{"sole automatically excluded", store.DirExpense, false, true, false, true},
		{"sole manually excluded", store.DirExpense, false, false, false, true},
		{"shared income", store.DirIncome, true, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payer := int64(1)
			members := []store.Member{{ID: 1, Name: "Anna"}, {ID: 2, Name: "Ben"}}
			row := BookingRow{
				Booking: store.Booking{
					ID: 1, Name: "Booking", AmountCents: 100000, Direction: tc.direction,
					Frequency: store.FreqMonthly, SplitMode: store.SplitEqual,
					Settle: tc.settle, PayerMemberID: &payer,
				},
				Members: members, Payer: members[0], Month: "2026-09",
				Carriers: members[:1],
				Splits:   []store.BookingSplit{{MemberID: 1}},
			}
			if tc.shared {
				row.Carriers = members
				row.Splits = append(row.Splits, store.BookingSplit{MemberID: 2})
			}
			if got := row.SharedWithoutSettlement(); got != tc.warning {
				t.Errorf("warning = %t, want %t", got, tc.warning)
			}
			var out bytes.Buffer
			if err := bookingRow(row).Render(t.Context(), &out); err != nil {
				t.Fatal(err)
			}
			body := out.String()
			split := strings.Index(body, `class="meta-cell meta-split"`)
			settle := strings.Index(body, `class="meta-cell meta-settle"`)
			class := strings.Index(body, `class="meta-cell meta-class"`)
			if split < 0 || settle <= split || class <= settle {
				t.Fatal("settlement column must directly follow the split column")
			}
			noSettle := T(t.Context(), "bookings.noSettle")
			if strings.Contains(body[settle:class], noSettle) != tc.excluded {
				t.Error("incorrect settlement indicator")
			}
			flags := strings.Index(body, `class="row-flags"`)
			meta := strings.Index(body, `class="row-meta"`)
			if strings.Contains(body[flags:meta], noSettle) {
				t.Error("settlement indicator still appears before the metadata columns")
			}
			wantRed := 0
			if tc.warning {
				wantRed = 2
			}
			if got := strings.Count(body, "badge-danger"); got != wantRed {
				t.Errorf("red badges = %d, want %d", got, wantRed)
			}
			if tc.shared && !tc.warning && !strings.Contains(body[split:settle], "badge-sky") {
				t.Error("ordinary shared badge lost its normal color")
			}
		})
	}
}

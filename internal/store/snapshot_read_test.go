package store

import "testing"

func TestInspectHouseholdKeepsAmountsAndSplitsInOneSnapshot(t *testing.T) {
	st, household, account, payment := bankFixture(t)
	ctx := t.Context()
	b, err := st.CreateBooking(ctx, Booking{
		HouseholdID: household, CategoryID: payment.CategoryID, Direction: DirExpense, Name: "Atomic synthetic plan",
		AmountCents: 101, Frequency: FreqMonthly, Interval: 1, CostNature: CostVariable, BudgetClass: ClassNeed, SplitMode: SplitFixed,
	}, []SplitInput{{MemberID: account.MemberID, Value: 101}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := range 30 {
			b.AmountCents = int64(200 + i)
			if err := st.SaveBooking(ctx, b, []SplitInput{{MemberID: account.MemberID, Value: float64(b.AmountCents)}}, nil); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for range 30 {
		snapshot, stats, err := st.InspectHousehold(ctx, household)
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Bookings) != stats.Bookings {
			t.Fatal("inventory came from a different snapshot")
		}
		for _, row := range snapshot.Bookings {
			if len(row.Splits) != 1 || row.Splits[0].Value != float64(row.Booking.AmountCents) {
				t.Fatal("mixed booking/allocation snapshots")
			}
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

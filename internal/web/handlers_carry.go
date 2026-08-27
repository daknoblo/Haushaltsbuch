package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// CarryRow is one booking waiting to be taken into the new year.
type CarryRow struct {
	Booking  store.Booking
	Category store.Category
}

// CarryVM is the yearly review: everything whose period is about to run out,
// so extending the book is a decision per booking rather than a chore.
type CarryVM struct {
	Year int
	Rows []CarryRow
}

// Pending reports whether anything is waiting.
func (v CarryVM) Pending() bool { return len(v.Rows) > 0 }

// YearStr is the target year as a string, for a form field.
func (v CarryVM) YearStr() string { return strconv.Itoa(v.Year) }

// buildCarryVM collects the recurring bookings that end before the year they
// should be carried into.
func (s *Server) buildCarryVM(ctx context.Context, householdID int64, cats []store.Category) (CarryVM, error) {
	bookings, err := s.store.ListBookings(ctx, householdID)
	if err != nil {
		return CarryVM{}, err
	}
	byID := make(map[int64]store.Category, len(cats))
	for _, c := range cats {
		byID[c.ID] = c
	}

	year := carryYear(time.Now())

	vm := CarryVM{Year: year}
	for _, b := range bookings {
		if carriable(b, year) {
			vm.Rows = append(vm.Rows, CarryRow{Booking: b, Category: byID[b.CategoryID]})
		}
	}
	return vm, nil
}

// carryYear is the year the book should reach. That is this one, until November,
// when planning the next one starts to make sense. Tying it to the calendar and
// not to the data is what makes the card disappear once everything is carried;
// a target that always sat one year past the last end date would be a button
// one could press forever, stretching the plan into a decade nobody meant.
func carryYear(now time.Time) int {
	if now.Month() >= time.November {
		return now.Year() + 1
	}
	return now.Year()
}

// carriable reports whether a booking is waiting for the new year: it recurs,
// and it stops at the end of a December earlier than the one being planned for.
//
// The turn of the year is what separates a book that simply runs out from a
// booking that was closed off on purpose — the March half of a price change
// ends mid-year, and carrying it would raise it from the dead alongside its own
// successor, counting the cost twice.
//
// Only the month is read, never the day: books written before the end date was
// stored as the last of the month carry a first of December instead, and they
// mean the same thing.
func carriable(b store.Booking, year int) bool {
	if !b.Frequency.Recurring() || len(b.EndsOn) < 7 || b.EndsOn[5:7] != "12" {
		return false
	}
	end, err := strconv.Atoi(b.EndsOn[:4])
	return err == nil && end < year
}

func (s *Server) handleCarryForward(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}

	year, err := strconv.Atoi(r.FormValue("year"))
	if err != nil || year < 2000 || year > 2100 {
		s.clientError(w, r, http.StatusBadRequest, "error.carryYear")
		return
	}
	ids := make([]int64, 0, len(r.Form["booking"]))
	for _, raw := range r.Form["booking"] {
		if id := parseID(raw); id != 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		s.clientError(w, r, http.StatusBadRequest, "error.carryNone")
		return
	}

	if err := s.store.ExtendBookings(r.Context(), active, ids, strconv.Itoa(year)+"-12-31"); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxRefresh(w)
}

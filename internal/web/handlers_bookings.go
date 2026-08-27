package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// amountOrKeep parses a submitted amount. A syntactically incomplete value
// (the auto-save fires on every keystroke, so "12," is normal) keeps the stored
// amount, while an out-of-range value is reported to the caller.
func amountOrKeep(submitted string, current int64) (int64, error) {
	cents, err := ParseCents(submitted)
	if errors.Is(err, ErrAmountRange) {
		return current, err
	}
	if err != nil {
		// Half-typed input is expected while the auto-save fires, so the stored
		// amount is kept instead of reporting an error.
		return current, nil //nolint:nilerr
	}
	return cents, nil
}

// defaultCategory picks the category a new booking starts with. A booking
// cannot exist without one, so the first matching category is used.
func defaultCategory(cats []store.Category, dir store.Direction) int64 {
	for _, c := range cats {
		if c.Classification == dir {
			return c.ID
		}
	}
	return 0
}

// handleBookingCreate opens the dialog on a fresh draft. Creating it up front is
// what lets every field save itself, so the dialog needs no save button.
func (s *Server) handleBookingCreate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	dir := store.Direction(r.URL.Query().Get("direction"))
	if !dir.Valid() {
		dir = store.DirExpense
	}

	form, err := s.bookingForm(ctx, active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	catID := defaultCategory(form.Categories, dir)
	if catID == 0 {
		s.clientError(w, r, http.StatusBadRequest, "error.categoryNeeded")
		return
	}
	if len(form.Members) == 0 {
		s.clientError(w, r, http.StatusBadRequest, "error.payerMissing")
		return
	}

	name := T(ctx, "bookings.newExpense")
	if dir == store.DirIncome {
		name = T(ctx, "bookings.newIncome")
	}
	month := NormalizeMonth(r.URL.Query().Get("m"))
	from, until := calendarYearBounds(month)
	// Who pays and who carries it is left open on purpose: a guess here is one
	// the user has to notice and undo.
	b := store.Booking{
		HouseholdID: active,
		CategoryID:  catID,
		Direction:   dir,
		Name:        name,
		Frequency:   store.FreqMonthly,
		Interval:    1,
		DuePoint:    store.DueStart,
		StartsOn:    from,
		EndsOn:      until,
		CostNature:  store.CostFix,
		BudgetClass: store.ClassNeed,
		SplitMode:   store.SplitEqual,
		Settle:      true,
	}

	created, err := s.store.CreateBooking(ctx, b, nil, nil)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.renderDialog(w, r, active, created.ID, month, true)
}

// handleBookingEdit renders the dialog for an existing booking.
func (s *Server) handleBookingEdit(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	s.renderDialog(w, r, active, parseID(r.PathValue("id")),
		NormalizeMonth(r.URL.Query().Get("m")), false)
}

// handleBookingDiscard drops a draft the dialog was closed on without a single
// edit. The row has to exist before it can save itself, so without this every
// mistaken click on "add" would leave a dead booking behind.
func (s *Server) handleBookingDiscard(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	b, err := s.store.GetBooking(ctx, active, parseID(r.PathValue("id")))
	if errors.Is(err, store.ErrNotFound) {
		// Deleting from inside the dialog closes it too, so there is nothing left.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !isBlankDraft(ctx, b) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.DeleteBooking(ctx, active, b.ID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxChanged(w)
}

// isBlankDraft reports whether a booking still holds nothing but the values the
// dialog created it with.
func isBlankDraft(ctx context.Context, b store.Booking) bool {
	return b.AmountCents == 0 && b.Note == "" &&
		(b.Name == "" || nameIsSuggested(ctx, b.Name))
}

// renderDialog assembles the booking dialog from the stored state, so what the
// user sees is always what was actually saved.
func (s *Server) renderDialog(w http.ResponseWriter, r *http.Request, householdID, id int64, month string, draft bool) {
	ctx := r.Context()
	b, err := s.store.GetBooking(ctx, householdID, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	form, err := s.bookingForm(ctx, householdID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	row := BookingRow{Booking: b, Month: month}
	if row.Splits, err = s.store.ListSplits(ctx, householdID, id); err != nil {
		s.serverError(w, r, err)
		return
	}
	if row.TagIDs, err = s.store.ListTagIDs(ctx, householdID, id); err != nil {
		s.serverError(w, r, err)
		return
	}
	if row.Overrides, err = s.store.ListOverrides(ctx, householdID, id); err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, c := range form.Categories {
		if c.ID == b.CategoryID {
			row.Category = c
		}
	}
	form.Row = row
	form.Draft = draft
	s.render(w, r, BookingDialog(form))
}

// handleBookingUpdate saves one edit and answers without any markup. Replacing
// the dialog would move the caret and could drop characters typed while the
// request was in flight, so the DOM is left alone.
func (s *Server) handleBookingUpdate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := parseID(r.PathValue("id"))

	b, err := s.store.GetBooking(ctx, active, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !s.parseForm(w, r) {
		return
	}

	b.Name = cleanName(r.FormValue("name"))
	b.Note = cleanName(r.FormValue("note"))
	amount, err := amountOrKeep(r.FormValue("amount"), b.AmountCents)
	if err != nil {
		s.clientError(w, r, http.StatusBadRequest, "error.amountRange")
		return
	}
	b.AmountCents = amount

	if dir := store.Direction(r.FormValue("direction")); dir.Valid() {
		b.Direction = dir
	}
	// The dialog offers a recurring switch rather than "once" as a rhythm.
	if r.FormValue("recurring") == "" {
		b.Frequency = store.FreqOnce
	} else {
		b.Frequency = store.Frequency(r.FormValue("frequency"))
		if !b.Frequency.Valid() || !b.Frequency.Recurring() {
			b.Frequency = store.FreqMonthly
		}
	}
	b.Interval = clampInterval(r.FormValue("interval"))
	b.DuePoint = store.DuePoint(r.FormValue("due_point"))
	if !b.DuePoint.Valid() {
		b.DuePoint = store.DueStart
	}
	// Cost nature, budget class and the settlement switch are only rendered for
	// an expense. Reading them for an income would reset all three to their
	// defaults on every keystroke, because a field that is not in the form
	// arrives empty and empty is not a valid value.
	if b.Direction == store.DirExpense {
		b.CostNature = store.CostNature(r.FormValue("cost_nature"))
		if !b.CostNature.Valid() {
			b.CostNature = store.CostFix
		}
		b.BudgetClass = store.BudgetClass(r.FormValue("budget_class"))
		if !b.BudgetClass.Valid() {
			b.BudgetClass = store.ClassNeed
		}
		b.Settle = r.FormValue("settle") != ""
	}
	b.SplitMode = store.SplitMode(r.FormValue("split_mode"))
	if !b.SplitMode.Valid() {
		b.SplitMode = store.SplitEqual
	}

	if b.Frequency.Recurring() {
		b.StartsOn = monthToDate(cleanMonth(r.FormValue("active_from")))
		b.EndsOn = monthToDate(cleanMonth(r.FormValue("active_until")))
	} else {
		b.StartsOn = cleanDate(r.FormValue("occurred_on"))
		b.EndsOn = ""
	}

	if catID, err := s.categoryFromName(ctx, active, b, r.FormValue("category")); err != nil {
		s.serverError(w, r, err)
		return
	} else if catID != 0 {
		b.CategoryID = catID
	}
	// A radio cannot be unticked, so the picker carries an explicit "nobody".
	// Without it a payer picked by mistake could never be taken back.
	if payer := parseID(r.FormValue("payer_member_id")); payer != 0 {
		b.PayerMemberID = &payer
	} else if r.Form.Has("payer_member_id") {
		b.PayerMemberID = nil
	}

	splits, err := s.splitsFromForm(r, b)
	if err != nil {
		s.clientError(w, r, http.StatusBadRequest, "error.invalidShare")
		return
	}
	if err := s.store.SaveBooking(ctx, b, splits, tagsFromForm(r)); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxChanged(w)
}

// categoryFromName resolves what was typed into the category field. The field
// saves itself on every keystroke, so a half-typed name has to leave the
// stored category alone; "geh" already picks Gehalt once nothing else starts
// like it. Returns 0 when nothing matches.
func (s *Server) categoryFromName(ctx context.Context, householdID int64, b store.Booking, typed string) (int64, error) {
	typed = strings.ToLower(strings.TrimSpace(typed))
	if typed == "" {
		return 0, nil
	}
	cats, err := s.store.ListCategories(ctx, householdID)
	if err != nil {
		return 0, err
	}

	var prefix, contains []int64
	for _, c := range cats {
		if c.Classification != b.Direction {
			continue
		}
		name := strings.ToLower(c.Name)
		switch {
		case name == typed:
			return c.ID, nil
		case strings.HasPrefix(name, typed):
			prefix = append(prefix, c.ID)
		case strings.Contains(name, typed):
			contains = append(contains, c.ID)
		}
	}
	if len(prefix) == 1 {
		return prefix[0], nil
	}
	if len(prefix) == 0 && len(contains) == 1 {
		return contains[0], nil
	}
	return 0, nil
}

// calendarYearBounds is the range a new recurring booking runs over: the whole
// year the user is looking at. Ending it in December is what turns the new year
// into a deliberate review instead of a plan nobody ever revisits.
func calendarYearBounds(month string) (from, until string) {
	year := NormalizeMonth(month)[:4]
	return year + "-01-01", year + "-12-31"
}

// clampInterval keeps a submitted recurrence interval sane.
func clampInterval(v string) int {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return 1
	}
	if n > 60 {
		return 60
	}
	return n
}

// monthToDate turns a YYYY-MM value into the first of that month, which is how
// an open-ended range is stored.
func monthToDate(m string) string {
	if m == "" {
		return ""
	}
	return m + "-01"
}

// tagsFromForm collects the checked tag ids.
func tagsFromForm(r *http.Request) []int64 {
	raw := r.Form["tag"]
	out := make([]int64, 0, len(raw))
	for _, v := range raw {
		if id := parseID(v); id != 0 {
			out = append(out, id)
		}
	}
	return out
}

// splitsFromForm rebuilds the split list from the submitted participation
// checkboxes and values. Each mode reads its own field, so a mode change can
// never reinterpret a figure that was entered for the other one.
func (s *Server) splitsFromForm(r *http.Request, b store.Booking) ([]store.SplitInput, error) {
	ctx := r.Context()
	members, err := s.store.ListMembers(ctx, b.HouseholdID)
	if err != nil {
		return nil, err
	}
	current, err := s.store.ListSplits(ctx, b.HouseholdID, b.ID)
	if err != nil {
		return nil, err
	}
	stored := make(map[int64]float64, len(current))
	for _, sp := range current {
		stored[sp.MemberID] = sp.Value
	}

	out := make([]store.SplitInput, 0, len(members))
	for _, m := range members {
		key := strconv.FormatInt(m.ID, 10)
		if r.FormValue("m_"+key) == "" {
			continue
		}
		val := stored[m.ID]
		switch b.SplitMode {
		case store.SplitPercent:
			val = keepOnBlank(r, "p_"+key, val, func(raw string) (float64, bool) {
				v, err := ParseFloatLoose(raw)
				return calc.ClampPercent(v), err == nil
			})
		case store.SplitFixed:
			val = keepOnBlank(r, "f_"+key, val, func(raw string) (float64, bool) {
				cents, err := ParseCents(raw)
				return float64(cents), err == nil
			})
		default: // equal splits ignore the value
			val = 0
		}
		out = append(out, store.SplitInput{MemberID: m.ID, Value: val})
	}
	return out, nil
}

// keepOnBlank keeps the stored value when the field was not submitted at all.
// An empty string parses as 0 without error, so without this a request that
// happens to omit the field would silently wipe every share.
func keepOnBlank(r *http.Request, name string, stored float64, parse func(string) (float64, bool)) float64 {
	raw := r.FormValue(name)
	if strings.TrimSpace(raw) == "" {
		return stored
	}
	if v, ok := parse(raw); ok {
		return v
	}
	return stored
}

func (s *Server) handleBookingDelete(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteBooking(r.Context(), active, parseID(r.PathValue("id"))); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxChanged(w)
}

// ---- amount overrides ----

// overrideFromForm reads the fields shared by create and update.
func overrideFromForm(r *http.Request) (store.BookingOverride, error) {
	cents, err := ParseCents(r.FormValue("amount"))
	if errors.Is(err, ErrAmountRange) {
		return store.BookingOverride{}, err
	}
	return store.BookingOverride{
		StartsOn:    cleanDate(r.FormValue("starts_on")),
		EndsOn:      cleanDate(r.FormValue("ends_on")),
		AmountCents: cents,
		Note:        cleanName(r.FormValue("note")),
	}, nil
}

func (s *Server) handleOverrideCreate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	o, err := overrideFromForm(r)
	if err != nil {
		s.clientError(w, r, http.StatusBadRequest, "error.amountRange")
		return
	}
	o.BookingID = parseID(r.PathValue("id"))
	if _, err := s.store.CreateOverride(r.Context(), active, o); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.renderDialog(w, r, active, o.BookingID, NormalizeMonth(r.FormValue("m")), false)
}

func (s *Server) handleOverrideUpdate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	o, err := overrideFromForm(r)
	if err != nil {
		s.clientError(w, r, http.StatusBadRequest, "error.amountRange")
		return
	}
	o.ID = parseID(r.PathValue("id"))
	if err := s.store.UpdateOverride(r.Context(), active, o); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxChanged(w)
}

func (s *Server) handleOverrideDelete(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	if err := s.store.DeleteOverride(r.Context(), active, parseID(r.PathValue("id"))); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.renderDialog(w, r, active, parseID(r.FormValue("booking_id")),
		NormalizeMonth(r.FormValue("m")), false)
}

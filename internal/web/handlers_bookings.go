package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

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
	payer := form.Members[0].ID
	b := store.Booking{
		HouseholdID:   active,
		CategoryID:    catID,
		PayerMemberID: &payer,
		Direction:     dir,
		Name:          name,
		Frequency:     store.FreqMonthly,
		Interval:      1,
		DuePoint:      store.DueStart,
		StartsOn:      month + "-01",
		CostNature:    store.CostFix,
		BudgetClass:   store.ClassNeed,
		SplitMode:     store.SplitEqual,
		Settle:        true,
	}

	// Everyone participates by default, which is what a shared household bill
	// almost always is.
	splits := make([]store.SplitInput, 0, len(form.Members))
	for _, m := range form.Members {
		splits = append(splits, store.SplitInput{MemberID: m.ID})
	}

	created, err := s.store.CreateBooking(ctx, b, splits, nil)
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
	b.CostNature = store.CostNature(r.FormValue("cost_nature"))
	if !b.CostNature.Valid() {
		b.CostNature = store.CostFix
	}
	b.BudgetClass = store.BudgetClass(r.FormValue("budget_class"))
	if !b.BudgetClass.Valid() {
		b.BudgetClass = store.ClassNeed
	}
	b.SplitMode = store.SplitMode(r.FormValue("split_mode"))
	if !b.SplitMode.Valid() {
		b.SplitMode = store.SplitEqual
	}
	b.Settle = r.FormValue("settle") != ""

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
	if payer := parseID(r.FormValue("payer_member_id")); payer != 0 {
		b.PayerMemberID = &payer
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
// checkboxes and values. Values that cannot be parsed keep their stored value.
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
			if v, err := ParseFloatLoose(r.FormValue("v_" + key)); err == nil {
				val = clampPercent(v)
			}
		case store.SplitFixed:
			if cents, err := ParseCents(r.FormValue("v_" + key)); err == nil {
				val = float64(cents)
			}
		default: // equal splits ignore the value
			val = 0
		}
		out = append(out, store.SplitInput{MemberID: m.ID, Value: val})
	}
	return out, nil
}

// clampPercent keeps a submitted percentage within 0-100.
func clampPercent(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 100:
		return 100
	default:
		return v
	}
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

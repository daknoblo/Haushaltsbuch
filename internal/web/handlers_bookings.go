package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"

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

	vmCtx, err := s.bookingContext(ctx, active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	catID := defaultCategory(vmCtx.Categories, dir)
	if catID == 0 {
		s.clientError(w, r, http.StatusBadRequest, "error.categoryNeeded")
		return
	}

	name := "Neue Ausgabe"
	if dir == store.DirIncome {
		name = "Neue Einnahme"
	}
	b := store.Booking{
		HouseholdID: active,
		CategoryID:  catID,
		Direction:   dir,
		Name:        name,
		Frequency:   store.FreqMonthly,
		Interval:    1,
		StartsOn:    CurrentMonth() + "-01",
		CostNature:  store.CostFix,
		BudgetClass: store.ClassNeed,
		SplitMode:   store.SplitEqual,
	}
	if sectionID := parseID(r.URL.Query().Get("section_id")); sectionID != 0 && dir == store.DirExpense {
		b.SectionID = &sectionID
	}

	// Default split: everyone participates equally.
	splits := make([]store.SplitInput, 0, len(vmCtx.Members))
	for _, m := range vmCtx.Members {
		splits = append(splits, store.SplitInput{MemberID: m.ID})
	}

	created, err := s.store.CreateBooking(ctx, b, splits, nil)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	stored, err := s.splitsOf(ctx, active, created.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// A freshly created row opens straight into the editor.
	s.render(w, r, BookingRowView(BookingRow{Booking: created, Splits: stored, Expanded: true}, vmCtx))
}

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
	b.Frequency = store.Frequency(r.FormValue("frequency"))
	if !b.Frequency.Valid() {
		b.Frequency = store.FreqMonthly
	}
	b.Interval = clampInterval(r.FormValue("interval"))
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

	if b.Frequency.Recurring() {
		b.StartsOn = monthToDate(cleanMonth(r.FormValue("active_from")))
		b.EndsOn = monthToDate(cleanMonth(r.FormValue("active_until")))
	} else {
		b.StartsOn = cleanDate(r.FormValue("occurred_on"))
		b.EndsOn = ""
	}

	if secID := parseID(r.FormValue("section_id")); secID != 0 {
		b.SectionID = &secID
	} else {
		b.SectionID = nil
	}
	if catID := parseID(r.FormValue("category_id")); catID != 0 {
		b.CategoryID = catID
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

	updated, err := s.store.GetBooking(ctx, active, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	stored, err := s.splitsOf(ctx, active, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	vmCtx, err := s.bookingContext(ctx, active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tagIDs, err := s.store.ListBookingTags(ctx, active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	row := BookingRow{
		Booking:  updated,
		Splits:   stored,
		TagIDs:   tagIDs[id],
		Expanded: r.FormValue("expanded") == "1",
	}
	s.render(w, r, BookingRowView(row, vmCtx))
}

// splitsOf returns the stored splits of one booking of a household.
func (s *Server) splitsOf(ctx context.Context, householdID, id int64) ([]store.BookingSplit, error) {
	all, err := s.store.ListSplitsForHousehold(ctx, householdID)
	if err != nil {
		return nil, err
	}
	return all[id], nil
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
	current, err := s.splitsOf(ctx, b.HouseholdID, b.ID)
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
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleBookingMove(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	delta, ok := parseDelta(r.FormValue("dir"))
	if !ok {
		s.clientError(w, r, http.StatusBadRequest, "error.invalidDir")
		return
	}
	ctx := r.Context()
	id := parseID(r.PathValue("id"))
	b, err := s.store.GetBooking(ctx, active, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	var sectionID int64
	if b.SectionID != nil {
		sectionID = *b.SectionID
	}
	if err := s.store.MoveBooking(ctx, active, sectionID, id, delta); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxRefresh(w)
}

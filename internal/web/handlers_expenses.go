package web

import (
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

func (s *Server) handleExpenseCreate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	e := store.Expense{
		HouseholdID: active,
		Name:        "Neue Ausgabe",
		Frequency:   store.FreqMonthly,
		CostNature:  store.CostFix,
		BudgetClass: store.ClassNeed,
		SplitMode:   store.SplitEqual,
		ActiveFrom:  CurrentMonth(),
	}
	if sectionID := parseID(r.URL.Query().Get("section_id")); sectionID != 0 {
		e.SectionID = &sectionID
	}

	// Default split: everyone participates equally.
	members, err := s.store.ListMembers(ctx, active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	splits := make([]store.SplitInput, 0, len(members))
	for _, m := range members {
		splits = append(splits, store.SplitInput{MemberID: m.ID})
	}

	created, err := s.store.CreateExpense(ctx, e, splits)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	stored, err := s.store.ListSplits(ctx, created.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	vmCtx, err := s.expenseContext(ctx, active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	// A freshly created row opens straight into the editor.
	s.render(w, r, ExpenseRowView(ExpenseRow{Expense: created, Splits: stored, Expanded: true}, vmCtx))
}

func (s *Server) handleExpenseUpdate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := parseID(r.PathValue("id"))

	e, err := s.store.GetExpense(ctx, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if e.HouseholdID != active {
		http.NotFound(w, r)
		return
	}
	if !s.parseForm(w, r) {
		return
	}

	e.Name = cleanName(r.FormValue("name"))
	amount, err := amountOrKeep(r.FormValue("amount"), e.AmountCents)
	if err != nil {
		s.clientError(w, r, http.StatusBadRequest, "error.amountRange")
		return
	}
	e.AmountCents = amount

	e.Frequency = store.Frequency(r.FormValue("frequency"))
	if !e.Frequency.Valid() {
		e.Frequency = store.FreqMonthly
	}
	e.CostNature = store.CostNature(r.FormValue("cost_nature"))
	if !e.CostNature.Valid() {
		e.CostNature = store.CostFix
	}
	e.BudgetClass = store.BudgetClass(r.FormValue("budget_class"))
	if !e.BudgetClass.Valid() {
		e.BudgetClass = store.ClassNeed
	}
	e.SplitMode = store.SplitMode(r.FormValue("split_mode"))
	if !e.SplitMode.Valid() {
		e.SplitMode = store.SplitEqual
	}

	e.IsOneOff = r.FormValue("is_oneoff") != ""
	e.OccurredOn = cleanDate(r.FormValue("occurred_on"))
	e.ActiveFrom = cleanMonth(r.FormValue("active_from"))
	e.ActiveUntil = cleanMonth(r.FormValue("active_until"))

	if secID := parseID(r.FormValue("section_id")); secID != 0 {
		e.SectionID = &secID
	} else {
		e.SectionID = nil
	}
	if catID := parseID(r.FormValue("category_id")); catID != 0 {
		e.CategoryID = &catID
	} else {
		e.CategoryID = nil
	}

	splits, err := s.splitsFromForm(r, e)
	if err != nil {
		s.clientError(w, r, http.StatusBadRequest, "error.invalidShare")
		return
	}
	if err := s.store.SaveExpense(ctx, e, splits); err != nil {
		s.writeStoreError(w, r, err)
		return
	}

	updated, err := s.store.GetExpense(ctx, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	stored, err := s.store.ListSplits(ctx, id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	vmCtx, err := s.expenseContext(ctx, active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	row := ExpenseRow{Expense: updated, Splits: stored, Expanded: r.FormValue("expanded") == "1"}
	s.render(w, r, ExpenseRowView(row, vmCtx))
}

// splitsFromForm rebuilds the split list from the submitted participation
// checkboxes and values. Values that cannot be parsed keep their stored value.
func (s *Server) splitsFromForm(r *http.Request, e store.Expense) ([]store.SplitInput, error) {
	ctx := r.Context()
	members, err := s.store.ListMembers(ctx, e.HouseholdID)
	if err != nil {
		return nil, err
	}
	current, err := s.store.ListSplits(ctx, e.ID)
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
		switch e.SplitMode {
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

func (s *Server) handleExpenseDelete(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteExpense(r.Context(), active, parseID(r.PathValue("id"))); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleExpenseMove(w http.ResponseWriter, r *http.Request) {
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
	e, err := s.store.GetExpense(ctx, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if e.HouseholdID != active {
		http.NotFound(w, r)
		return
	}
	var sectionID int64
	if e.SectionID != nil {
		sectionID = *e.SectionID
	}
	if err := s.store.MoveExpense(ctx, active, sectionID, id, delta); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxRefresh(w)
}

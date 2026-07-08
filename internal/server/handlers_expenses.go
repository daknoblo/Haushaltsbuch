package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
	"github.com/daknoblo/Haushaltsbuch/internal/web"
)

func (s *Server) handleExpenseCreate(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if active == 0 {
		http.Error(w, "Kein aktiver Haushalt", http.StatusBadRequest)
		return
	}

	e := store.Expense{
		HouseholdID: active,
		Name:        "Neue Ausgabe",
		Frequency:   store.FreqMonthly,
		CostNature:  store.CostFix,
		BudgetClass: store.ClassNeed,
		SplitMode:   store.SplitEqual,
		ActiveFrom:  web.CurrentMonth(),
	}
	if sectionID := parseID(r.URL.Query().Get("section_id")); sectionID != 0 {
		e.SectionID = &sectionID
	}

	created, err := s.store.CreateExpense(e)
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	// Default split: everyone participates equally.
	members, err := s.store.ListMembers(active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, m := range members {
		_ = s.store.AddSplitMember(created.ID, m.ID)
	}

	splits, err := s.store.ListSplits(created.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	ctx, err := s.expenseContext(active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, web.ExpenseRowView(web.ExpenseRow{Expense: created, Splits: splits}, ctx))
}

func (s *Server) handleExpenseUpdate(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	e, err := s.store.GetExpense(id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Ungültige Eingabe", http.StatusBadRequest)
		return
	}

	e.Name = strings.TrimSpace(r.FormValue("name"))
	e.AmountCents, _ = web.ParseCents(r.FormValue("amount"))

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
	e.OccurredOn = strings.TrimSpace(r.FormValue("occurred_on"))
	e.ActiveFrom = strings.TrimSpace(r.FormValue("active_from"))
	e.ActiveUntil = strings.TrimSpace(r.FormValue("active_until"))

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

	if err := s.store.UpdateExpense(e); err != nil {
		s.serverError(w, r, err)
		return
	}

	// Rebuild splits from the submitted participation checkboxes and values.
	members, err := s.store.ListMembers(e.HouseholdID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	for _, m := range members {
		key := strconv.FormatInt(m.ID, 10)
		if r.FormValue("m_"+key) == "" {
			_ = s.store.RemoveSplitMember(id, m.ID)
			continue
		}
		var val float64
		switch e.SplitMode {
		case store.SplitPercent:
			val, _ = web.ParseFloatLoose(r.FormValue("v_" + key))
		case store.SplitFixed:
			cents, _ := web.ParseCents(r.FormValue("v_" + key))
			val = float64(cents)
		}
		_ = s.store.SetSplitValue(id, m.ID, val)
	}

	updated, err := s.store.GetExpense(id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	splits, err := s.store.ListSplits(id)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	ctx, err := s.expenseContext(e.HouseholdID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, web.ExpenseRowView(web.ExpenseRow{Expense: updated, Splits: splits}, ctx))
}

func (s *Server) handleExpenseDelete(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	if err := s.store.DeleteExpense(id); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

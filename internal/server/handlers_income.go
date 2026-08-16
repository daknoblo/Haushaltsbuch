package server

import (
	"errors"
	"net/http"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
	"github.com/daknoblo/Haushaltsbuch/internal/web"
)

func (s *Server) handleIncomeCreate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	member := parseID(r.URL.Query().Get("member"))
	if member == 0 {
		http.Error(w, "Person fehlt", http.StatusBadRequest)
		return
	}
	month := web.NormalizeMonth(r.URL.Query().Get("m"))

	// CreateIncome only inserts when the member belongs to the household.
	in, err := s.store.CreateIncome(r.Context(), active, member, month, "", 0)
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "Unbekannte Person", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, web.IncomeLineView(in))
}

func (s *Server) handleIncomeUpdate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	id := parseID(r.PathValue("id"))

	in, err := s.store.GetIncome(ctx, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if in.HouseholdID != active {
		http.NotFound(w, r)
		return
	}
	if !s.parseForm(w, r) {
		return
	}

	name := cleanName(r.FormValue("name"))
	amount, err := amountOrKeep(r.FormValue("amount"), in.AmountCents)
	if err != nil {
		http.Error(w, "Betrag außerhalb des zulässigen Bereichs", http.StatusBadRequest)
		return
	}
	if err := s.store.UpdateIncome(ctx, active, id, name, amount); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	in.Name = name
	in.AmountCents = amount
	s.render(w, r, web.IncomeLineView(in))
}

func (s *Server) handleIncomeDelete(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteIncome(r.Context(), active, parseID(r.PathValue("id"))); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleIncomeCopy(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	from := web.NormalizeMonth(r.URL.Query().Get("from"))
	to := web.NormalizeMonth(r.URL.Query().Get("to"))

	_, err := s.store.CopyIncomes(r.Context(), active, from, to)
	if errors.Is(err, store.ErrCopyTargetNotEmpty) {
		http.Error(w, "Der Zielmonat enthält bereits Einnahmen", http.StatusConflict)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	hxRefresh(w)
}

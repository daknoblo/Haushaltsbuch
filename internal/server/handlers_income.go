package server

import (
	"errors"
	"net/http"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
	"github.com/daknoblo/Haushaltsbuch/internal/web"
)

func (s *Server) handleIncomeCreate(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if active == 0 {
		http.Error(w, "Kein aktiver Haushalt", http.StatusBadRequest)
		return
	}
	member := parseID(r.URL.Query().Get("member"))
	if member == 0 {
		http.Error(w, "Person fehlt", http.StatusBadRequest)
		return
	}
	// Only members of the active household may receive income lines.
	m, err := s.store.GetMember(member)
	if errors.Is(err, store.ErrNotFound) || (err == nil && m.HouseholdID != active) {
		http.Error(w, "Unbekannte Person", http.StatusBadRequest)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	month := web.NormalizeMonth(r.URL.Query().Get("m"))

	in, err := s.store.CreateIncome(active, member, month, "", 0)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, web.IncomeLineView(in))
}

func (s *Server) handleIncomeUpdate(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	in, err := s.store.GetIncome(id)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	name := cleanName(r.FormValue("name"))
	amount, _ := web.ParseCents(r.FormValue("amount"))
	if err := s.store.UpdateIncome(id, name, amount); err != nil {
		s.serverError(w, r, err)
		return
	}
	in.Name = name
	in.AmountCents = amount
	s.render(w, r, web.IncomeLineView(in))
}

func (s *Server) handleIncomeDelete(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	if err := s.store.DeleteIncome(id); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleIncomeCopy(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if active == 0 {
		http.Error(w, "Kein aktiver Haushalt", http.StatusBadRequest)
		return
	}
	from := web.NormalizeMonth(r.URL.Query().Get("from"))
	to := web.NormalizeMonth(r.URL.Query().Get("to"))
	if _, err := s.store.CopyIncomes(active, from, to); err != nil {
		s.serverError(w, r, err)
		return
	}
	hxRefresh(w)
}

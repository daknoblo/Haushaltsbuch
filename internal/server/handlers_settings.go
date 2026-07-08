package server

import (
	"net/http"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/web"
)

var memberColors = []string{"#2563eb", "#db2777", "#059669", "#d97706", "#7c3aed", "#0891b2"}

// ---- households ----

func (s *Server) handleHouseholdCreate(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Name fehlt", http.StatusBadRequest)
		return
	}
	h, err := s.store.CreateHouseholdSeeded(name)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	activeID, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, web.HouseholdRowView(h, activeID))
}

func (s *Server) handleHouseholdRename(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	name := strings.TrimSpace(r.FormValue("name"))
	if id == 0 || name == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.RenameHousehold(id, name); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHouseholdActivate(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.FormValue("id"))
	if id != 0 {
		if err := s.store.SetActiveHousehold(id); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	hxRefresh(w)
}

func (s *Server) handleHouseholdDelete(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	active, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.DeleteHousehold(id); err != nil {
		s.serverError(w, r, err)
		return
	}
	if id == active {
		hs, err := s.store.ListHouseholds()
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if len(hs) > 0 {
			_ = s.store.SetActiveHousehold(hs[0].ID)
		}
	}
	hxRefresh(w)
}

// ---- members ----

func (s *Server) handleMemberCreate(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if active == 0 {
		http.Error(w, "Kein aktiver Haushalt", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Name fehlt", http.StatusBadRequest)
		return
	}
	existing, _ := s.store.ListMembers(active)
	color := memberColors[len(existing)%len(memberColors)]
	m, err := s.store.CreateMember(active, name, color)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, web.MemberRowView(m))
}

func (s *Server) handleMemberUpdate(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	name := strings.TrimSpace(r.FormValue("name"))
	color := strings.TrimSpace(r.FormValue("color"))
	if id == 0 || name == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.UpdateMember(id, name, color); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMemberDelete(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	if err := s.store.DeleteMember(id); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- sections ----

func (s *Server) handleSectionCreate(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if active == 0 {
		http.Error(w, "Kein aktiver Haushalt", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Name fehlt", http.StatusBadRequest)
		return
	}
	sec, err := s.store.CreateSection(active, name)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, web.SectionRowView(sec))
}

func (s *Server) handleSectionRename(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	name := strings.TrimSpace(r.FormValue("name"))
	if id == 0 || name == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.RenameSection(id, name); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSectionDelete(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	if err := s.store.DeleteSection(id); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- categories ----

func (s *Server) handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	active, err := s.store.ActiveHouseholdID()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if active == 0 {
		http.Error(w, "Kein aktiver Haushalt", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "Name fehlt", http.StatusBadRequest)
		return
	}
	c, err := s.store.CreateCategory(active, name)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, web.CategoryRowView(c))
}

func (s *Server) handleCategoryRename(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	name := strings.TrimSpace(r.FormValue("name"))
	if id == 0 || name == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.RenameCategory(id, name); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCategoryDelete(w http.ResponseWriter, r *http.Request) {
	id := parseID(r.PathValue("id"))
	if err := s.store.DeleteCategory(id); err != nil {
		s.serverError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

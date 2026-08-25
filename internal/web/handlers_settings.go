package web

import (
	"errors"
	"net/http"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

var memberColors = []string{"#2563eb", "#db2777", "#059669", "#d97706", "#7c3aed", "#0891b2"}

// ---- households ----

func (s *Server) handleHouseholdCreate(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	name := cleanName(r.FormValue("name"))
	if name == "" {
		s.clientError(w, r, http.StatusBadRequest, "error.nameMissing")
		return
	}
	ctx := r.Context()
	h, err := s.store.CreateHouseholdSeeded(ctx, name)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	activeID, err := s.store.ActiveHouseholdID(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, HouseholdRowView(h, activeID))
}

func (s *Server) handleHouseholdRename(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	id := parseID(r.PathValue("id"))
	name := cleanName(r.FormValue("name"))
	if id == 0 || name == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.RenameHousehold(r.Context(), id, name); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHouseholdActivate(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	id := parseID(r.FormValue("id"))
	if id != 0 {
		// Reject unknown ids instead of pointing the app at a missing household.
		if _, err := s.store.GetHousehold(ctx, id); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		if err := s.store.SetActiveHousehold(ctx, id); err != nil {
			s.serverError(w, r, err)
			return
		}
	}
	hxRefresh(w)
}

func (s *Server) handleHouseholdDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := parseID(r.PathValue("id"))
	active, err := s.store.ActiveHouseholdID(ctx)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.store.DeleteHousehold(ctx, id); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if id == active {
		hs, err := s.store.ListHouseholds(ctx)
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if len(hs) > 0 {
			if err := s.store.SetActiveHousehold(ctx, hs[0].ID); err != nil {
				s.serverError(w, r, err)
				return
			}
		}
	}
	hxRefresh(w)
}

func (s *Server) handleHouseholdMove(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	delta, ok := parseDelta(r.FormValue("dir"))
	if !ok {
		s.clientError(w, r, http.StatusBadRequest, "error.invalidDir")
		return
	}
	if err := s.store.MoveHousehold(r.Context(), parseID(r.PathValue("id")), delta); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxRefresh(w)
}

// ---- members ----

func (s *Server) handleMemberCreate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	name := cleanName(r.FormValue("name"))
	if name == "" {
		s.clientError(w, r, http.StatusBadRequest, "error.nameMissing")
		return
	}
	ctx := r.Context()
	existing, err := s.store.ListMembers(ctx, active)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	color := memberColors[len(existing)%len(memberColors)]
	m, err := s.store.CreateMember(ctx, active, name, color)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, MemberRowView(m))
}

func (s *Server) handleMemberUpdate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	ctx := r.Context()
	id := parseID(r.PathValue("id"))
	name := cleanName(r.FormValue("name"))
	if id == 0 || name == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	m, err := s.store.GetMember(ctx, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if err := s.store.UpdateMember(ctx, active, id, name, colorOrKeep(r.FormValue("color"), m.Color)); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMemberDelete(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteMember(r.Context(), active, parseID(r.PathValue("id"))); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleMemberMove(w http.ResponseWriter, r *http.Request) {
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
	if err := s.store.MoveMember(r.Context(), active, parseID(r.PathValue("id")), delta); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxRefresh(w)
}

// ---- sections ----

func (s *Server) handleSectionCreate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	name := cleanName(r.FormValue("name"))
	if name == "" {
		s.clientError(w, r, http.StatusBadRequest, "error.nameMissing")
		return
	}
	sec, err := s.store.CreateSection(r.Context(), active, name)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, SectionRowView(sec))
}

func (s *Server) handleSectionRename(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	id := parseID(r.PathValue("id"))
	name := cleanName(r.FormValue("name"))
	if id == 0 || name == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.RenameSection(r.Context(), active, id, name); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSectionDelete(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteSection(r.Context(), active, parseID(r.PathValue("id"))); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleSectionMove(w http.ResponseWriter, r *http.Request) {
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
	if err := s.store.MoveSection(r.Context(), active, parseID(r.PathValue("id")), delta); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxRefresh(w)
}

// ---- categories ----

// categoryFormFields reads the fields shared by create and update.
func categoryFormFields(r *http.Request) (name string, class store.Direction, color string) {
	name = cleanName(r.FormValue("name"))
	class = store.Direction(r.FormValue("classification"))
	if !class.Valid() {
		class = store.DirExpense
	}
	return name, class, cleanColor(r.FormValue("color"))
}

func (s *Server) handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	name, class, color := categoryFormFields(r)
	if name == "" {
		s.clientError(w, r, http.StatusBadRequest, "error.nameMissing")
		return
	}
	c, err := s.store.CreateCategory(r.Context(), active, name, class, color)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, CategoryRowView(c, 0))
}

func (s *Server) handleCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	id := parseID(r.PathValue("id"))
	name, class, color := categoryFormFields(r)
	if id == 0 || name == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.UpdateCategory(r.Context(), active, id, name, class, color); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCategoryDelete(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	err := s.store.DeleteCategory(r.Context(), active, parseID(r.PathValue("id")))
	if errors.Is(err, store.ErrCategoryInUse) {
		s.clientError(w, r, http.StatusConflict, "error.categoryUsed")
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ---- tags ----

func (s *Server) handleTagCreate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	name := cleanName(r.FormValue("name"))
	if name == "" {
		s.clientError(w, r, http.StatusBadRequest, "error.nameMissing")
		return
	}
	t, err := s.store.CreateTag(r.Context(), active, name, cleanColor(r.FormValue("color")))
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, TagRowView(t))
}

func (s *Server) handleTagUpdate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	id := parseID(r.PathValue("id"))
	name := cleanName(r.FormValue("name"))
	if id == 0 || name == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.store.RenameTag(r.Context(), active, id, name, cleanColor(r.FormValue("color"))); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTagDelete(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteTag(r.Context(), active, parseID(r.PathValue("id"))); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusOK)
}

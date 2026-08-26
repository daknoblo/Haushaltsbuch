package web

import (
	"errors"
	"net/http"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

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
	color := store.MemberColor(len(existing))
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
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	// A person without a name cannot be told apart in a split, so an empty one
	// is refused rather than silently ignored.
	if name == "" {
		s.clientError(w, r, http.StatusBadRequest, "error.nameMissing")
		return
	}
	m, err := s.store.GetMember(ctx, active, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if err := s.store.UpdateMember(ctx, active, id, name, colorOrKeep(r.FormValue("color"), m.Color)); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxChanged(w)
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

// ---- categories ----

// categoryFromForm reads the fields shared by create and update. An icon left
// unset is guessed from the name, so a category never renders blank.
func categoryFromForm(r *http.Request) store.Category {
	c := store.Category{
		Name:           cleanName(r.FormValue("name")),
		Classification: store.Direction(r.FormValue("classification")),
		Color:          cleanColor(r.FormValue("color")),
		Icon:           cleanIcon(r.FormValue("icon")),
	}
	if !c.Classification.Valid() {
		c.Classification = store.DirExpense
	}
	if c.Icon == "" {
		c.Icon = GuessIcon(c.Name)
	}
	return c
}

func (s *Server) handleCategoryCreate(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	c := categoryFromForm(r)
	if c.Name == "" {
		s.clientError(w, r, http.StatusBadRequest, "error.nameMissing")
		return
	}
	if _, err := s.store.CreateCategory(r.Context(), active, c); err != nil {
		s.serverError(w, r, err)
		return
	}
	hxRefresh(w)
}

// handleCategorySuggest creates one of the proposed categories, matched by name
// so a hand-crafted request cannot invent an arbitrary one.
func (s *Server) handleCategorySuggest(w http.ResponseWriter, r *http.Request) {
	active, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	if !s.parseForm(w, r) {
		return
	}
	want := cleanName(r.FormValue("name"))
	for _, sug := range suggestCategories(nil) {
		if sug.Name != want {
			continue
		}
		c := store.Category{Name: sug.Name, Classification: sug.Class, Color: sug.Color, Icon: sug.Icon}
		if _, err := s.store.CreateCategory(r.Context(), active, c); err != nil {
			s.serverError(w, r, err)
			return
		}
		hxRefresh(w)
		return
	}
	s.clientError(w, r, http.StatusBadRequest, "error.invalidInput")
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
	c := categoryFromForm(r)
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	if c.Name == "" {
		s.clientError(w, r, http.StatusBadRequest, "error.nameMissing")
		return
	}
	if err := s.store.UpdateCategory(r.Context(), active, id, c); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxChanged(w)
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
	hxRefresh(w)
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
	if id == 0 {
		http.NotFound(w, r)
		return
	}
	if name == "" {
		s.clientError(w, r, http.StatusBadRequest, "error.nameMissing")
		return
	}
	if err := s.store.RenameTag(r.Context(), active, id, name, cleanColor(r.FormValue("color"))); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	hxChanged(w)
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

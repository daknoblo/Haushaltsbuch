package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

var hexColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

// household resolves the household a request works on: the one named in the
// query, or the active one. A script that only ever runs against one household
// can then leave the parameter out entirely.
func (s *Server) household(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.URL.Query().Get("household")
	if raw == "" {
		active, err := s.store.ActiveHouseholdID(r.Context())
		if err != nil {
			s.fail(w, r, err)
			return 0, false
		}
		if active == 0 {
			writeError(w, http.StatusNotFound, "kein aktiver Haushalt")
			return 0, false
		}
		return active, true
	}

	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "household muss eine Zahl sein")
		return 0, false
	}
	if _, err := s.store.GetHousehold(r.Context(), id); err != nil {
		s.fail(w, r, err)
		return 0, false
	}
	return id, true
}

// ---- reference data ----

type householdOut struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Active bool   `json:"active"`
}

func (s *Server) handleHouseholds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	households, err := s.store.ListHouseholds(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	active, err := s.store.ActiveHouseholdID(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := make([]householdOut, 0, len(households))
	for _, h := range households {
		out = append(out, householdOut{ID: h.ID, Name: h.Name, Active: h.ID == active})
	}
	writeJSON(w, http.StatusOK, map[string]any{"households": out})
}

type categoryOut struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Classification string `json:"classification"`
	Color          string `json:"color"`
	Icon           string `json:"icon"`
}

func (s *Server) handleCategories(w http.ResponseWriter, r *http.Request) {
	id, ok := s.household(w, r)
	if !ok {
		return
	}
	cats, err := s.store.ListCategories(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := make([]categoryOut, 0, len(cats))
	for _, c := range cats {
		out = append(out, asCategoryOut(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"categories": out})
}

// categoryIn is a category as a caller writes it.
type categoryIn struct {
	Household      *int64  `json:"household"`
	Name           *string `json:"name"`
	Classification *string `json:"classification"`
	Color          *string `json:"color"`
	Icon           *string `json:"icon"`
}

// handleCreateCategory adds a category, or hands back the one that already
// carries the name. A booking cannot be filed without a category, so a job
// that sets up its own has to be able to run twice; the name is the natural
// key because that is what the booking endpoint resolves against.
func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var in categoryIn
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "ungültiger JSON-Body: "+err.Error())
		return
	}
	id, ok := s.householdFor(w, r, in.Household)
	if !ok {
		return
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name fehlt")
		return
	}
	name := clip(*in.Name)

	cats, err := s.store.ListCategories(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, c := range cats {
		if equalName(c.Name, name) {
			writeJSON(w, http.StatusOK, asCategoryOut(c))
			return
		}
	}

	class := store.DirExpense
	if in.Classification != nil {
		if d := store.Direction(strings.TrimSpace(*in.Classification)); d.Valid() {
			class = d
		} else {
			writeError(w, http.StatusBadRequest, "classification muss income oder expense sein")
			return
		}
	}

	c := store.Category{Name: name, Classification: class}
	if in.Color != nil {
		if col := strings.ToLower(strings.TrimSpace(*in.Color)); hexColor.MatchString(col) {
			c.Color = col
		} else if col != "" {
			writeError(w, http.StatusBadRequest, "color muss ein Hex-Wert wie #6366f1 sein")
			return
		}
	}
	if in.Icon != nil {
		c.Icon = strings.TrimSpace(*in.Icon)
	}

	// An unset color or icon is filled in when the page renders, so a caller
	// that only knows the name still gets a category that looks like the rest.
	created, err := s.store.CreateCategory(r.Context(), id, c)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, asCategoryOut(created))
}

func asCategoryOut(c store.Category) categoryOut {
	return categoryOut{
		ID: c.ID, Name: c.Name, Classification: string(c.Classification),
		Color: c.Color, Icon: c.Icon,
	}
}

type memberOut struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := s.household(w, r)
	if !ok {
		return
	}
	members, err := s.store.ListMembers(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := make([]memberOut, 0, len(members))
	for _, m := range members {
		out = append(out, memberOut{ID: m.ID, Name: m.Name, Color: m.Color})
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (s *Server) handleTags(w http.ResponseWriter, r *http.Request) {
	id, ok := s.household(w, r)
	if !ok {
		return
	}
	tags, err := s.store.ListTags(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := make([]memberOut, 0, len(tags))
	for _, t := range tags {
		out = append(out, memberOut{ID: t.ID, Name: t.Name, Color: t.Color})
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": out})
}

// ---- report ----

type reportOut struct {
	Month           string           `json:"month"`
	Member          int64            `json:"member"`
	IncomeCents     int64            `json:"income_cents"`
	ExpenseCents    int64            `json:"expense_cents"`
	BalanceCents    int64            `json:"balance_cents"`
	FixedCents      int64            `json:"fixed_cents"`
	VariableCents   int64            `json:"variable_cents"`
	UnassignedCents int64            `json:"unassigned_cents"`
	SavingsRate     float64          `json:"savings_rate"`
	ByBudgetClass   map[string]int64 `json:"by_budget_class"`
	Categories      []labeledOut     `json:"categories"`
}

type labeledOut struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
	Cents int64  `json:"cents"`
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	id, ok := s.household(w, r)
	if !ok {
		return
	}
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	if _, err := time.Parse("2006-01", month); err != nil {
		writeError(w, http.StatusBadRequest, "month muss YYYY-MM sein")
		return
	}
	member, ok := s.member(w, r, id)
	if !ok {
		return
	}

	data, err := s.load(r, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rep := calc.BuildMonthReport(data, month, member)

	classes := make(map[string]int64, len(rep.ByBudgetClass))
	for k, v := range rep.ByBudgetClass {
		classes[string(k)] = v
	}
	cats := make([]labeledOut, 0, len(rep.Categories))
	for _, c := range rep.Categories {
		cats = append(cats, labeledOut{ID: c.Key, Label: c.Label, Cents: c.Cents})
	}

	writeJSON(w, http.StatusOK, reportOut{
		Month: rep.Month, Member: rep.Member,
		IncomeCents: rep.IncomeCents, ExpenseCents: rep.ExpenseCents,
		BalanceCents: rep.BalanceCents, FixedCents: rep.FixedCents(),
		VariableCents: rep.VariableCents(), UnassignedCents: rep.UnassignedCents,
		SavingsRate: rep.SavingsRate(), ByBudgetClass: classes, Categories: cats,
	})
}

// member reads an optional person scope, so a report can answer "what does this
// cost me" as well as what it costs the household.
func (s *Server) member(w http.ResponseWriter, r *http.Request, householdID int64) (int64, bool) {
	raw := r.URL.Query().Get("member")
	if raw == "" {
		return calc.Everyone, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "member muss eine Zahl sein")
		return 0, false
	}
	members, err := s.store.ListMembers(r.Context(), householdID)
	if err != nil {
		s.fail(w, r, err)
		return 0, false
	}
	for _, m := range members {
		if m.ID == id {
			return id, true
		}
	}
	writeError(w, http.StatusBadRequest, "member gehört nicht zu diesem Haushalt")
	return 0, false
}

// load assembles what the calc package needs for a report.
func (s *Server) load(r *http.Request, householdID int64) (calc.Data, error) {
	ctx := r.Context()
	var (
		d   calc.Data
		err error
	)
	if d.Members, err = s.store.ListMembers(ctx, householdID); err != nil {
		return calc.Data{}, err
	}
	if d.Categories, err = s.store.ListCategories(ctx, householdID); err != nil {
		return calc.Data{}, err
	}
	if d.Tags, err = s.store.ListTags(ctx, householdID); err != nil {
		return calc.Data{}, err
	}
	if d.Bookings, err = s.store.ListBookings(ctx, householdID); err != nil {
		return calc.Data{}, err
	}
	if d.Splits, err = s.store.ListSplitsForHousehold(ctx, householdID); err != nil {
		return calc.Data{}, err
	}
	if d.TagLinks, err = s.store.ListBookingTags(ctx, householdID); err != nil {
		return calc.Data{}, err
	}
	if d.Overrides, err = s.store.ListOverridesForHousehold(ctx, householdID); err != nil {
		return calc.Data{}, err
	}
	return d, nil
}

// resolveCategory accepts a name as readily as an id, because a script reads
// better with "Miete" in it than with a number nobody can check.
func (s *Server) resolveCategory(r *http.Request, householdID int64, id int64, name string, dir store.Direction) (int64, error) {
	cats, err := s.store.ListCategories(r.Context(), householdID)
	if err != nil {
		return 0, err
	}
	for _, c := range cats {
		if (id != 0 && c.ID == id) || (id == 0 && name != "" && equalName(c.Name, name)) {
			if c.Classification != dir {
				return 0, errBadCategoryDirection
			}
			return c.ID, nil
		}
	}
	return 0, errUnknownCategory
}

// resolveMember does the same for a person.
func (s *Server) resolveMember(r *http.Request, householdID int64, id int64, name string) (int64, error) {
	members, err := s.store.ListMembers(r.Context(), householdID)
	if err != nil {
		return 0, err
	}
	for _, m := range members {
		if (id != 0 && m.ID == id) || (id == 0 && name != "" && equalName(m.Name, name)) {
			return m.ID, nil
		}
	}
	return 0, errUnknownMember
}

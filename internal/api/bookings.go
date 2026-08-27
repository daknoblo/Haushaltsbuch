package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/daknoblo/Haushaltsbuch/internal/calc"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// Reasons a reference in a request cannot be used. They are values rather than
// strings so the handler can turn each into the right status.
var (
	errUnknownCategory      = errors.New("kategorie nicht gefunden")
	errBadCategoryDirection = errors.New("kategorie passt nicht zur richtung")
	errUnknownMember        = errors.New("person nicht gefunden")
)

// maxNameLen mirrors what the browser form accepts, so the same figure cannot
// be entered one way and refused the other.
const maxNameLen = 120

// shareIn is one person's part of a booking. Value means nothing for an equal
// split, a percentage for percent and cents for fixed — the same rule the
// dialog follows.
type shareIn struct {
	Member   *int64   `json:"member"`
	Name     *string  `json:"name"`
	Value    *float64 `json:"value"`
	MemberID int64    `json:"-"`
}

// bookingIn is a booking as a caller writes it. Every field is a pointer so an
// update can tell "leave this alone" from "set it to zero".
type bookingIn struct {
	ExternalID  *string    `json:"external_id"`
	Household   *int64     `json:"household"`
	Direction   *string    `json:"direction"`
	Name        *string    `json:"name"`
	Note        *string    `json:"note"`
	Amount      *float64   `json:"amount"`
	AmountCents *int64     `json:"amount_cents"`
	Category    *string    `json:"category"`
	CategoryID  *int64     `json:"category_id"`
	Frequency   *string    `json:"frequency"`
	Interval    *int       `json:"interval"`
	DuePoint    *string    `json:"due_point"`
	Date        *string    `json:"date"`
	ActiveFrom  *string    `json:"active_from"`
	ActiveUntil *string    `json:"active_until"`
	CostNature  *string    `json:"cost_nature"`
	BudgetClass *string    `json:"budget_class"`
	SplitMode   *string    `json:"split_mode"`
	Settle      *bool      `json:"settle"`
	Payer       *string    `json:"payer"`
	PayerID     *int64     `json:"payer_id"`
	Shares      *[]shareIn `json:"shares"`
	Tags        *[]string  `json:"tags"`
}

// bookingOut is a booking as the API reports it. Amounts travel as cents so no
// figure is ever rounded on the way through a float.
type bookingOut struct {
	ID           int64     `json:"id"`
	ExternalID   string    `json:"external_id,omitempty"`
	Household    int64     `json:"household"`
	Direction    string    `json:"direction"`
	Name         string    `json:"name"`
	Note         string    `json:"note,omitempty"`
	AmountCents  int64     `json:"amount_cents"`
	MonthlyCents int64     `json:"monthly_cents"`
	Category     string    `json:"category"`
	CategoryID   int64     `json:"category_id"`
	Frequency    string    `json:"frequency"`
	Interval     int       `json:"interval"`
	DuePoint     string    `json:"due_point"`
	StartsOn     string    `json:"starts_on,omitempty"`
	EndsOn       string    `json:"ends_on,omitempty"`
	CostNature   string    `json:"cost_nature"`
	BudgetClass  string    `json:"budget_class"`
	SplitMode    string    `json:"split_mode"`
	Settle       bool      `json:"settle"`
	PayerID      int64     `json:"payer_id,omitempty"`
	Shares       []shareOu `json:"shares"`
	Tags         []int64   `json:"tags"`
	UpdatedAt    string    `json:"updated_at"`
}

type shareOu struct {
	Member int64   `json:"member"`
	Value  float64 `json:"value"`
}

func (s *Server) handleListBookings(w http.ResponseWriter, r *http.Request) {
	id, ok := s.household(w, r)
	if !ok {
		return
	}
	month := r.URL.Query().Get("month")
	if month != "" {
		if _, err := time.Parse("2006-01", month); err != nil {
			writeError(w, http.StatusBadRequest, "month muss YYYY-MM sein")
			return
		}
	}

	data, err := s.load(r, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cats := categoryNames(data)
	tags, err := s.store.ListBookingTags(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}

	out := make([]bookingOut, 0, len(data.Bookings))
	for _, b := range data.Bookings {
		// Without a month the caller wants the book, with one only what counts
		// in it — which is not the same as what was created that month.
		if month != "" && !calc.ActiveIn(b, month) {
			continue
		}
		out = append(out, toBookingOut(b, cats, data.Splits[b.ID], tags[b.ID], month))
	}
	writeJSON(w, http.StatusOK, map[string]any{"bookings": out})
}

func (s *Server) handleGetBooking(w http.ResponseWriter, r *http.Request) {
	id, ok := s.household(w, r)
	if !ok {
		return
	}
	b, ok := s.bookingFromPath(w, r, id)
	if !ok {
		return
	}
	s.writeBooking(w, r, http.StatusOK, id, b)
}

func (s *Server) handleCreateBooking(w http.ResponseWriter, r *http.Request) {
	var in bookingIn
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "ungültiger JSON-Body: "+err.Error())
		return
	}
	id, ok := s.householdFor(w, r, in.Household)
	if !ok {
		return
	}

	// An external id turns a create into an upsert, so a job that runs twice
	// updates its own booking instead of filing a second one.
	if in.ExternalID != nil && strings.TrimSpace(*in.ExternalID) != "" {
		existing, err := s.store.GetBookingByExternalID(r.Context(), id, strings.TrimSpace(*in.ExternalID))
		if err == nil {
			s.saveBooking(w, r, id, existing, in, http.StatusOK)
			return
		}
		if !errors.Is(err, store.ErrNotFound) {
			s.fail(w, r, err)
			return
		}
	}

	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name fehlt")
		return
	}
	if in.Category == nil && in.CategoryID == nil {
		writeError(w, http.StatusBadRequest, "category oder category_id fehlt")
		return
	}
	s.saveBooking(w, r, id, defaultBooking(id), in, http.StatusCreated)
}

func (s *Server) handleUpdateBooking(w http.ResponseWriter, r *http.Request) {
	var in bookingIn
	if err := decode(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "ungültiger JSON-Body: "+err.Error())
		return
	}
	id, ok := s.householdFor(w, r, in.Household)
	if !ok {
		return
	}
	b, ok := s.bookingFromPath(w, r, id)
	if !ok {
		return
	}
	s.saveBooking(w, r, id, b, in, http.StatusOK)
}

func (s *Server) handleDeleteBooking(w http.ResponseWriter, r *http.Request) {
	id, ok := s.household(w, r)
	if !ok {
		return
	}
	b, ok := s.bookingFromPath(w, r, id)
	if !ok {
		return
	}
	if err := s.store.DeleteBooking(r.Context(), id, b.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": b.ID})
}

// bookingFromPath accepts the numeric id or, prefixed with "ext:", the one the
// caller gave it — so a script never has to remember what the app assigned.
func (s *Server) bookingFromPath(w http.ResponseWriter, r *http.Request, householdID int64) (store.Booking, bool) {
	raw := r.PathValue("id")
	if ext, found := strings.CutPrefix(raw, "ext:"); found {
		b, err := s.store.GetBookingByExternalID(r.Context(), householdID, ext)
		if err != nil {
			s.fail(w, r, err)
			return store.Booking{}, false
		}
		return b, true
	}

	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "id muss eine Zahl oder ext:<kennzeichen> sein")
		return store.Booking{}, false
	}
	b, err := s.store.GetBooking(r.Context(), householdID, id)
	if err != nil {
		s.fail(w, r, err)
		return store.Booking{}, false
	}
	return b, true
}

// householdFor prefers the household named in the body over the query, so a
// batch of bodies can each name their own.
func (s *Server) householdFor(w http.ResponseWriter, r *http.Request, fromBody *int64) (int64, bool) {
	if fromBody != nil {
		if _, err := s.store.GetHousehold(r.Context(), *fromBody); err != nil {
			s.fail(w, r, err)
			return 0, false
		}
		return *fromBody, true
	}
	return s.household(w, r)
}

// defaultBooking is what a new booking starts from, matching what the dialog
// creates so the two ways in agree.
func defaultBooking(householdID int64) store.Booking {
	return store.Booking{
		HouseholdID: householdID,
		Direction:   store.DirExpense,
		Frequency:   store.FreqMonthly,
		Interval:    1,
		DuePoint:    store.DueStart,
		CostNature:  store.CostFix,
		BudgetClass: store.ClassNeed,
		SplitMode:   store.SplitEqual,
		Settle:      true,
	}
}

// saveBooking applies the fields a caller set and writes the result.
func (s *Server) saveBooking(w http.ResponseWriter, r *http.Request, householdID int64, b store.Booking, in bookingIn, okStatus int) {
	if err := applyBooking(&b, in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if in.CategoryID != nil || in.Category != nil {
		catID, name := int64(0), ""
		if in.CategoryID != nil {
			catID = *in.CategoryID
		}
		if in.Category != nil {
			name = *in.Category
		}
		id, err := s.resolveCategory(r, householdID, catID, name, b.Direction)
		if err != nil {
			s.referenceError(w, r, err)
			return
		}
		b.CategoryID = id
	}
	if b.CategoryID == 0 {
		writeError(w, http.StatusBadRequest, "category oder category_id fehlt")
		return
	}

	if in.PayerID != nil || in.Payer != nil {
		payer, ok := s.resolvePayer(w, r, householdID, in)
		if !ok {
			return
		}
		b.PayerMemberID = payer
	}

	splits, ok := s.resolveShares(w, r, householdID, in)
	if !ok {
		return
	}
	tagIDs, ok := s.resolveTags(w, r, householdID, in)
	if !ok {
		return
	}

	var err error
	if b.ID == 0 {
		b, err = s.store.CreateBooking(r.Context(), b, splits, tagIDs)
	} else {
		if splits == nil {
			if splits, err = currentSplits(r, s, householdID, b.ID); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		if tagIDs == nil {
			if tagIDs, err = s.store.ListTagIDs(r.Context(), householdID, b.ID); err != nil {
				s.fail(w, r, err)
				return
			}
		}
		err = s.store.SaveBooking(r.Context(), b, splits, tagIDs)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeBooking(w, r, okStatus, householdID, b)
}

// currentSplits keeps the shares a booking already had when a caller did not
// mention them, so a partial update does not quietly unassign it.
func currentSplits(r *http.Request, s *Server, householdID, bookingID int64) ([]store.SplitInput, error) {
	stored, err := s.store.ListSplits(r.Context(), householdID, bookingID)
	if err != nil {
		return nil, err
	}
	out := make([]store.SplitInput, 0, len(stored))
	for _, sp := range stored {
		out = append(out, store.SplitInput{MemberID: sp.MemberID, Value: sp.Value})
	}
	return out, nil
}

func (s *Server) referenceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errUnknownCategory), errors.Is(err, errBadCategoryDirection),
		errors.Is(err, errUnknownMember):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) resolvePayer(w http.ResponseWriter, r *http.Request, householdID int64, in bookingIn) (*int64, bool) {
	id, name := int64(0), ""
	if in.PayerID != nil {
		id = *in.PayerID
	}
	if in.Payer != nil {
		name = *in.Payer
	}
	// An explicit null or empty name is how a caller takes the payer back.
	if id == 0 && name == "" {
		return nil, true
	}
	resolved, err := s.resolveMember(r, householdID, id, name)
	if err != nil {
		s.referenceError(w, r, err)
		return nil, false
	}
	return &resolved, true
}

func (s *Server) resolveShares(w http.ResponseWriter, r *http.Request, householdID int64, in bookingIn) ([]store.SplitInput, bool) {
	if in.Shares == nil {
		return nil, true
	}
	out := make([]store.SplitInput, 0, len(*in.Shares))
	for i, sh := range *in.Shares {
		id, name := int64(0), ""
		if sh.Member != nil {
			id = *sh.Member
		}
		if sh.Name != nil {
			name = *sh.Name
		}
		resolved, err := s.resolveMember(r, householdID, id, name)
		if err != nil {
			writeError(w, http.StatusBadRequest, "shares["+strconv.Itoa(i)+"]: "+err.Error())
			return nil, false
		}
		var value float64
		if sh.Value != nil {
			value = *sh.Value
		}
		out = append(out, store.SplitInput{MemberID: resolved, Value: value})
	}
	return out, true
}

func (s *Server) resolveTags(w http.ResponseWriter, r *http.Request, householdID int64, in bookingIn) ([]int64, bool) {
	if in.Tags == nil {
		return nil, true
	}
	tags, err := s.store.ListTags(r.Context(), householdID)
	if err != nil {
		s.fail(w, r, err)
		return nil, false
	}
	out := make([]int64, 0, len(*in.Tags))
	for _, want := range *in.Tags {
		var found int64
		for _, t := range tags {
			if equalName(t.Name, want) {
				found = t.ID
				break
			}
		}
		if found == 0 {
			writeError(w, http.StatusBadRequest, "tag nicht gefunden: "+want)
			return nil, false
		}
		out = append(out, found)
	}
	return out, true
}

// applyBooking copies the fields a caller set onto the booking, rejecting a
// value no enum knows rather than falling back to a default the caller never
// asked for.
func applyBooking(b *store.Booking, in bookingIn) error {
	if in.ExternalID != nil {
		b.ExternalID = strings.TrimSpace(*in.ExternalID)
	}
	if in.Name != nil {
		b.Name = clip(*in.Name)
	}
	if in.Note != nil {
		b.Note = clip(*in.Note)
	}
	if in.AmountCents != nil {
		b.AmountCents = *in.AmountCents
	} else if in.Amount != nil {
		b.AmountCents = int64(*in.Amount*100 + copySign(0.5, *in.Amount))
	}
	if b.AmountCents < 0 {
		return errors.New("amount darf nicht negativ sein")
	}

	if in.Direction != nil {
		d := store.Direction(*in.Direction)
		if !d.Valid() {
			return errors.New("direction muss income oder expense sein")
		}
		b.Direction = d
	}
	if in.Frequency != nil {
		f := store.Frequency(*in.Frequency)
		if !f.Valid() {
			return errors.New("frequency muss once, weekly, monthly, quarterly oder yearly sein")
		}
		b.Frequency = f
	}
	if in.Interval != nil {
		if *in.Interval < 1 || *in.Interval > 60 {
			return errors.New("interval muss zwischen 1 und 60 liegen")
		}
		b.Interval = *in.Interval
	}
	if in.DuePoint != nil {
		p := store.DuePoint(*in.DuePoint)
		if !p.Valid() {
			return errors.New("due_point muss start, mid oder end sein")
		}
		b.DuePoint = p
	}
	if in.CostNature != nil {
		c := store.CostNature(*in.CostNature)
		if !c.Valid() {
			return errors.New("cost_nature muss fix oder variable sein")
		}
		b.CostNature = c
	}
	if in.BudgetClass != nil {
		c := store.BudgetClass(*in.BudgetClass)
		if !c.Valid() {
			return errors.New("budget_class muss need, want oder saving sein")
		}
		b.BudgetClass = c
	}
	if in.SplitMode != nil {
		m := store.SplitMode(*in.SplitMode)
		if !m.Valid() {
			return errors.New("split_mode muss equal, percent oder fixed sein")
		}
		b.SplitMode = m
	}
	if in.Settle != nil {
		b.Settle = *in.Settle
	}
	return applyDates(b, in)
}

// applyDates keeps the two ways of dating a booking apart: a one-off falls on a
// date, a recurring one runs over a range.
func applyDates(b *store.Booking, in bookingIn) error {
	if in.Date != nil {
		d, err := cleanDate(*in.Date)
		if err != nil {
			return errors.New("date muss YYYY-MM-DD sein")
		}
		b.StartsOn, b.EndsOn = d, ""
	}
	if in.ActiveFrom != nil {
		d, err := cleanDate(*in.ActiveFrom)
		if err != nil {
			return errors.New("active_from muss YYYY-MM-DD sein")
		}
		b.StartsOn = d
	}
	if in.ActiveUntil != nil {
		d, err := cleanDate(*in.ActiveUntil)
		if err != nil {
			return errors.New("active_until muss YYYY-MM-DD sein")
		}
		b.EndsOn = d
	}
	if !b.Frequency.Recurring() && b.StartsOn == "" {
		return errors.New("eine einmalige Buchung braucht ein date")
	}
	return nil
}

func cleanDate(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return "", err
	}
	return v, nil
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= maxNameLen {
		return s
	}
	return string([]rune(s)[:maxNameLen])
}

func copySign(mag, sign float64) float64 {
	if sign < 0 {
		return -mag
	}
	return mag
}

func equalName(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func categoryNames(d calc.Data) map[int64]string {
	out := make(map[int64]string, len(d.Categories))
	for _, c := range d.Categories {
		out[c.ID] = c.Name
	}
	return out
}

func (s *Server) writeBooking(w http.ResponseWriter, r *http.Request, status int, householdID int64, b store.Booking) {
	fresh, err := s.store.GetBooking(r.Context(), householdID, b.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	cats, err := s.store.ListCategories(r.Context(), householdID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	names := make(map[int64]string, len(cats))
	for _, c := range cats {
		names[c.ID] = c.Name
	}
	splits, err := s.store.ListSplits(r.Context(), householdID, fresh.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	tagIDs, err := s.store.ListTagIDs(r.Context(), householdID, fresh.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, status, toBookingOut(fresh, names, splits, tagIDs, ""))
}

func toBookingOut(b store.Booking, cats map[int64]string, splits []store.BookingSplit, tagIDs []int64, month string) bookingOut {
	if month == "" {
		month = time.Now().Format("2006-01")
	}
	shares := make([]shareOu, 0, len(splits))
	for _, sp := range splits {
		shares = append(shares, shareOu{Member: sp.MemberID, Value: sp.Value})
	}
	if tagIDs == nil {
		tagIDs = []int64{}
	}

	out := bookingOut{
		ID: b.ID, ExternalID: b.ExternalID, Household: b.HouseholdID,
		Direction: string(b.Direction), Name: b.Name, Note: b.Note,
		AmountCents:  b.AmountCents,
		MonthlyCents: calc.MonthlyCents(b, nil, month),
		Category:     cats[b.CategoryID], CategoryID: b.CategoryID,
		Frequency: string(b.Frequency), Interval: b.Interval,
		DuePoint: string(b.DuePoint), StartsOn: b.StartsOn, EndsOn: b.EndsOn,
		CostNature: string(b.CostNature), BudgetClass: string(b.BudgetClass),
		SplitMode: string(b.SplitMode), Settle: b.Settle,
		Shares: shares, Tags: tagIDs, UpdatedAt: b.UpdatedAt,
	}
	if b.PayerMemberID != nil {
		out.PayerID = *b.PayerMemberID
	}
	return out
}

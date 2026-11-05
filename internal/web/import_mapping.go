package web

import (
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

const (
	importPreviewTTL  = 15 * time.Minute
	maxImportPreviews = 4
)

var (
	errImportPreview     = errors.New("import mapping expired")
	errImportPreviewFull = errors.New("too many pending import mappings")
	errImportMapping     = errors.New("each source needs a distinct destination account")
)

type pendingImport struct {
	household int64
	digest    string
	filename  string
	expires   time.Time
	sources   []bankimport.SourceAccount
}

type importPreviews struct {
	mu         sync.Mutex
	generation uint64
	items      map[string]pendingImport
}

func (p *importPreviews) prune() {
	for token, item := range p.items {
		if !time.Now().Before(item.expires) {
			delete(p.items, token)
		}
	}
}

func (p *importPreviews) epoch() uint64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.generation
}

func (p *importPreviews) put(generation uint64, item pendingImport) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	if generation != p.generation {
		return "", errImportPreview
	}
	if len(p.items) >= maxImportPreviews {
		return "", errImportPreviewFull
	}
	if p.items == nil {
		p.items = make(map[string]pendingImport)
	}
	token := rand.Text()
	item.expires = time.Now().Add(importPreviewTTL)
	p.items[token] = item
	return token, nil
}

func (p *importPreviews) get(token string, household int64) (pendingImport, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	item, ok := p.items[token]
	if !ok {
		return pendingImport{}, errImportPreview
	}
	if item.household != household {
		return pendingImport{}, store.ErrNotFound
	}
	return item, nil
}

func (p *importPreviews) commit(token string, household int64, fn func(pendingImport) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	item, ok := p.items[token]
	if !ok {
		return errImportPreview
	}
	if item.household != household {
		return store.ErrNotFound
	}
	if err := fn(item); err != nil {
		return err
	}
	delete(p.items, token)
	return nil
}

func (p *importPreviews) current(generation uint64, fn func() error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation != p.generation {
		return errImportPreview
	}
	return fn()
}

// Serialize confirmation with data replacement so old previews cannot target
// restored rows whose numeric IDs happen to have been reused.
func (p *importPreviews) reset(fn func() error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := fn(); err != nil {
		return err
	}
	p.generation++
	clear(p.items)
	return nil
}

// ImportMappingLink resumes an uncommitted mapping within its original household.
type ImportMappingLink struct {
	URL      string
	Filename string
}

func (p *importPreviews) list(household int64) []ImportMappingLink {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	var links []ImportMappingLink
	for token, item := range p.items {
		if item.household == household {
			links = append(links, ImportMappingLink{URL: importMappingURL(token), Filename: item.filename})
		}
	}
	sort.Slice(links, func(i, j int) bool { return links[i].URL < links[j].URL })
	return links
}

func importMappingURL(token string) string {
	return "/imports/mapping?preview=" + url.QueryEscape(token)
}

// ImportMappingVM contains no original file or full source-account identifiers.
type ImportMappingVM struct {
	Token    string
	Filename string
	Sources  []bankimport.SourceAccount
	Accounts []store.BankAccount
	Members  []store.Member
}

func (s *Server) handleImportMapping(w http.ResponseWriter, r *http.Request) {
	household, ok := s.requireActiveHousehold(w, r)
	if !ok {
		return
	}
	token := r.URL.Query().Get("preview")
	item, err := s.importPreviews.get(token, household)
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	nav, err := s.buildNav(r, "imports", "/imports", false)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if nav.ActiveHousehold.ID != household {
		s.clientError(w, r, http.StatusConflict, "import.householdChanged")
		return
	}
	vm := ImportMappingVM{Token: token, Filename: item.filename, Sources: item.sources}
	vm.Accounts, err = s.store.ListBankAccounts(r.Context(), household)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	vm.Members, err = s.store.ListMembers(r.Context(), household)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, ImportMappingPage(nav, vm))
}

func (s *Server) handleImportMappingConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	household, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	month := ""
	err := s.importPreviews.commit(r.FormValue("preview"), household, func(item pendingImport) error {
		batches := make([]store.BankImportBatch, 0, len(item.sources))
		seen := make(map[int64]bool, len(item.sources))
		for i, source := range item.sources {
			account := parseID(r.FormValue("target_" + strconv.Itoa(i)))
			if account == 0 || seen[account] {
				return errImportMapping
			}
			seen[account] = true
			batches = append(batches, store.BankImportBatch{
				Import:   store.BankImport{AccountID: account, Digest: item.digest, Filename: item.filename},
				Payments: source.Payments,
			})
		}
		if err := s.store.CreateBankImports(r.Context(), household, batches); err != nil {
			return err
		}
		month = item.sources[0].Payments[0].BookedOn[:7]
		return nil
	})
	if err != nil {
		s.bankError(w, r, err)
		return
	}
	redirectImportMonth(w, r, month)
}

func (s *Server) handleImportMappingCancel(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	household, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	if err := s.importPreviews.commit(r.FormValue("preview"), household, func(pendingImport) error { return nil }); err != nil {
		s.bankError(w, r, err)
		return
	}
	http.Redirect(w, r, "/imports", http.StatusSeeOther)
}

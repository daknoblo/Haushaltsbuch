package web

import (
	"crypto/rand"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/bankimport"
	"github.com/daknoblo/Haushaltsbuch/internal/logsafe"
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
	household       int64
	digest          string
	filename        string
	expires         time.Time
	sources         []bankimport.SourceAccount
	selectedAccount int64
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

func (p *importPreviews) list(household int64) []ImportMappingVM {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prune()
	var links []ImportMappingVM
	for token, item := range p.items {
		if item.household == household {
			links = append(links, ImportMappingVM{Token: token, Filename: item.filename, Sources: item.sources, SelectedAccount: item.selectedAccount})
		}
	}
	sort.Slice(links, func(i, j int) bool { return links[i].Token < links[j].Token })
	return links
}

func importMappingURL(token string) string {
	return "/imports?preview=" + url.QueryEscape(token) + "#" + importMappingID(token)
}

func importMappingID(token string) string { return "import-mapping-" + token }

// ImportMappingVM contains no original file or full source-account identifiers.
type ImportMappingVM struct {
	Token           string
	Filename        string
	Sources         []bankimport.SourceAccount
	Accounts        []store.BankAccount
	Members         []store.Member
	Selected        []int64
	SelectedAccount int64
}

// URL resumes this mapping on the normal Import page.
func (vm ImportMappingVM) URL() string { return importMappingURL(vm.Token) }

// ID anchors refreshes without replacing an edited mapping form.
func (vm ImportMappingVM) ID() string { return importMappingID(vm.Token) }

func mappingSuggestions(vm ImportMappingVM) []int64 {
	normalize := func(s string) string { return strings.Join(strings.Fields(strings.ToLower(s)), " ") }
	counts := map[string]int{}
	for _, source := range vm.Sources {
		counts[normalize(source.Name)]++
	}
	selected := make([]int64, len(vm.Sources))
	for i, source := range vm.Sources {
		name := normalize(source.Name)
		if name == "" || counts[name] != 1 {
			continue
		}
		var matches []int64
		for _, account := range vm.Accounts {
			if normalize(account.Name) == name {
				matches = append(matches, account.ID)
				if account.ID == vm.SelectedAccount {
					selected[i] = account.ID
				}
			}
		}
		if selected[i] == 0 && len(matches) == 1 {
			selected[i] = matches[0]
		}
	}
	return selected
}

func (s *Server) importPageError(w http.ResponseWriter, r *http.Request, problem error) {
	if r.Header.Get("Accept") == "application/json" {
		s.bankError(w, r, problem)
		return
	}
	nav, err := s.buildNav(r, "imports", "/imports", true)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	vm, err := s.buildImportVM(r.Context(), nav.ActiveHousehold.ID, nav.Month)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	status, key := bankErrorStatus(problem)
	if status == http.StatusInternalServerError {
		s.logger.Error("import request failed", "err", logsafe.Value(problem.Error()))
	} else {
		s.logger.Warn("import request rejected", "reason", key)
	}
	vm.MappingMessage = T(r.Context(), string(key))
	for i := range vm.PendingMappings {
		mapping := &vm.PendingMappings[i]
		if mapping.Token != r.FormValue("preview") {
			continue
		}
		for j := range mapping.Selected {
			mapping.Selected[j] = parseID(r.FormValue("target_" + strconv.Itoa(j)))
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	s.render(w, r, ImportsPage(nav, vm))
}

func (s *Server) handleImportMapping(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("preview")
	// Legacy links only return to Import; untrusted tokens never become anchors.
	if len(token) < 20 || len(token) > 128 || strings.Trim(token, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-") != "" {
		http.Redirect(w, r, "/imports?mapping_notice=expired", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, importMappingURL(token), http.StatusSeeOther) // #nosec G710 -- fixed /imports path, escaped query and allowlisted fragment token.
}

func (s *Server) handleImportMappingConfirm(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	household, ok := s.importHousehold(w, r)
	if !ok {
		return
	}
	outcome := uploadOutcome{Status: "imported"}
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
		comparison, err := s.store.ReconcileBankImports(r.Context(), household, batches)
		if err != nil {
			return err
		}
		outcome.Digest, outcome.Accounts = item.digest, len(item.sources)
		outcome.Added, outcome.Existing = comparison.Added, comparison.Existing
		outcome.Preserved = comparison.Preserved
		for _, source := range item.sources {
			outcome.Transactions += len(source.Payments)
		}
		outcome.URL = importMonthURL(comparison.ReviewMonth)
		return nil
	})
	if err != nil {
		s.importPageError(w, r, err)
		return
	}
	s.uploadResult(w, r, outcome)
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
		s.importPageError(w, r, err)
		return
	}
	if r.Header.Get("Accept") == "application/json" {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/imports", http.StatusSeeOther)
}

// Package server wires the HTTP routing and handlers for Haushaltsbuch.
package server

import (
	"log/slog"
	"net/http"

	"github.com/a-h/templ"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
	"github.com/daknoblo/Haushaltsbuch/internal/web"
)

// Server holds the dependencies shared by all handlers.
type Server struct {
	store  *store.Store
	logger *slog.Logger
}

// New creates a Server.
func New(st *store.Store, logger *slog.Logger) *Server {
	return &Server{store: st, logger: logger}
}

// Handler builds the HTTP handler with all routes and middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static assets.
	assets := http.StripPrefix("/assets/", cacheControl(http.FileServer(http.FS(web.AssetsFS()))))
	mux.Handle("GET /assets/", assets)

	// Health.
	mux.HandleFunc("GET /healthz", s.handleHealth)

	// Pages.
	mux.HandleFunc("GET /{$}", s.handleOverview)
	mux.HandleFunc("GET /expenses", s.handleExpenses)
	mux.HandleFunc("GET /income", s.handleIncome)
	mux.HandleFunc("GET /statistics", s.handleStatistics)
	mux.HandleFunc("GET /settings", s.handleSettings)

	// Households.
	mux.HandleFunc("POST /households", s.handleHouseholdCreate)
	mux.HandleFunc("POST /households/activate", s.handleHouseholdActivate)
	mux.HandleFunc("POST /households/{id}", s.handleHouseholdRename)
	mux.HandleFunc("POST /households/{id}/delete", s.handleHouseholdDelete)

	// Members.
	mux.HandleFunc("POST /members", s.handleMemberCreate)
	mux.HandleFunc("POST /members/{id}", s.handleMemberUpdate)
	mux.HandleFunc("POST /members/{id}/delete", s.handleMemberDelete)

	// Sections.
	mux.HandleFunc("POST /sections", s.handleSectionCreate)
	mux.HandleFunc("POST /sections/{id}", s.handleSectionRename)
	mux.HandleFunc("POST /sections/{id}/delete", s.handleSectionDelete)

	// Categories.
	mux.HandleFunc("POST /categories", s.handleCategoryCreate)
	mux.HandleFunc("POST /categories/{id}", s.handleCategoryRename)
	mux.HandleFunc("POST /categories/{id}/delete", s.handleCategoryDelete)

	// Expenses.
	mux.HandleFunc("POST /expenses/new", s.handleExpenseCreate)
	mux.HandleFunc("POST /expenses/{id}", s.handleExpenseUpdate)
	mux.HandleFunc("POST /expenses/{id}/delete", s.handleExpenseDelete)

	// Income.
	mux.HandleFunc("POST /income/new", s.handleIncomeCreate)
	mux.HandleFunc("POST /income/copy", s.handleIncomeCopy)
	mux.HandleFunc("POST /income/{id}", s.handleIncomeUpdate)
	mux.HandleFunc("POST /income/{id}/delete", s.handleIncomeDelete)

	// PDF export.
	mux.HandleFunc("GET /export/overview.pdf", s.handleExportOverview)
	mux.HandleFunc("GET /export/statistics.pdf", s.handleExportStatistics)
	mux.HandleFunc("GET /export/expenses.pdf", s.handleExportExpenses)

	// Outermost middleware first: a panic anywhere below is still recovered and
	// still logged with the resulting status code.
	return s.recoverer(s.logRequests(securityHeaders(s.sameOrigin(limitBody(compressResponses(mux))))))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// render writes a templ component as an HTML response. Responses contain
// personal financial data and are therefore never cached.
func (s *Server) render(w http.ResponseWriter, r *http.Request, c templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := c.Render(r.Context(), w); err != nil {
		s.logger.Error("render failed", "err", err, "path", r.URL.Path)
	}
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.Error("request failed", "err", err, "path", r.URL.Path, "method", r.Method)
	http.Error(w, "Interner Serverfehler", http.StatusInternalServerError)
}

// cacheControl marks the embedded static assets as privately cacheable. They
// change only with a new binary, so a short lifetime is enough to avoid
// re-downloads without serving stale files after an upgrade.
func cacheControl(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, max-age=3600")
		next.ServeHTTP(w, r)
	})
}

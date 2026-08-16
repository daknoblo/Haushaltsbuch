// Package server wires the HTTP routing and handlers for Haushaltsbuch.
package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
	"github.com/daknoblo/Haushaltsbuch/internal/version"
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
	mux.HandleFunc("POST /households/{id}/move", s.handleHouseholdMove)

	// Members.
	mux.HandleFunc("POST /members", s.handleMemberCreate)
	mux.HandleFunc("POST /members/{id}", s.handleMemberUpdate)
	mux.HandleFunc("POST /members/{id}/delete", s.handleMemberDelete)
	mux.HandleFunc("POST /members/{id}/move", s.handleMemberMove)

	// Sections.
	mux.HandleFunc("POST /sections", s.handleSectionCreate)
	mux.HandleFunc("POST /sections/{id}", s.handleSectionRename)
	mux.HandleFunc("POST /sections/{id}/delete", s.handleSectionDelete)
	mux.HandleFunc("POST /sections/{id}/move", s.handleSectionMove)

	// Categories.
	mux.HandleFunc("POST /categories", s.handleCategoryCreate)
	mux.HandleFunc("POST /categories/{id}", s.handleCategoryRename)
	mux.HandleFunc("POST /categories/{id}/delete", s.handleCategoryDelete)

	// Expenses.
	mux.HandleFunc("POST /expenses/new", s.handleExpenseCreate)
	mux.HandleFunc("POST /expenses/{id}", s.handleExpenseUpdate)
	mux.HandleFunc("POST /expenses/{id}/delete", s.handleExpenseDelete)
	mux.HandleFunc("POST /expenses/{id}/move", s.handleExpenseMove)

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

// handleHealth reports readiness. It touches the database so that the container
// health check fails when the data volume becomes unavailable.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		s.logger.Error("health check failed", "err", err)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
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

// cacheControl marks the embedded static assets as immutable. Templates link
// them with a version query, so a new binary produces new URLs and clients pick
// up changed files immediately instead of waiting for a cache entry to expire.
// Development builds keep the same version string across rebuilds, so there the
// assets must not be cached at all.
func cacheControl(next http.Handler) http.Handler {
	value := "private, max-age=31536000, immutable"
	if version.Version == "dev" || version.Version == "" {
		value = "no-cache"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

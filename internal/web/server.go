// Package web wires the HTTP routing, middleware and handlers for
// Haushaltsbuch and renders the templ views.
package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"

	"github.com/daknoblo/Haushaltsbuch/internal/i18n"
	"github.com/daknoblo/Haushaltsbuch/internal/store"
	"github.com/daknoblo/Haushaltsbuch/internal/version"
)

// Server holds the dependencies shared by all handlers.
type Server struct {
	store   *store.Store
	logger  *slog.Logger
	limiter *rateLimiter
}

// New creates a Server.
func New(st *store.Store, logger *slog.Logger) *Server {
	return &Server{store: st, logger: logger, limiter: newRateLimiter()}
}

// Handler builds the HTTP handler with all routes and middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static assets.
	assets := http.StripPrefix("/static/", cacheControl(http.FileServer(http.FS(AssetsFS()))))
	mux.Handle("GET /static/", assets)

	// Health, readiness and build metadata.
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /version", s.handleVersion)

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

	// Order follows the repository standard: recover, log, cap the body, set
	// security headers, reject cross-origin writes, then throttle. Language
	// resolution sits near the top so that rejections are translated too.
	return s.recoverer(s.logRequests(withLang(limitBody(securityHeaders(s.sameOrigin(s.rateLimit(compressResponses(mux))))))))
}

// handleHealth is the liveness probe. It answers without touching the database
// so that a stalled query cannot make the container be restarted.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleReady is the readiness probe and reports whether the store is usable.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.store.Ping(ctx); err != nil {
		s.logger.Error("readiness check failed", "err", err)
		http.Error(w, "database unavailable", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

// handleVersion reports the injected build metadata.
func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"version": version.Version,
		"commit":  version.Commit,
		"date":    version.Date,
	})
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
	s.clientError(w, r, http.StatusInternalServerError, "error.internal")
}

// clientError writes a translated plain-text error. Internal details stay in
// the log so that responses never leak paths or SQL.
func (s *Server) clientError(w http.ResponseWriter, r *http.Request, code int, key i18n.Key) {
	http.Error(w, i18n.C(r.Context(), key), code)
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

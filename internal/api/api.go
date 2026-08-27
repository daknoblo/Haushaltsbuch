// Package api serves the machine-facing HTTP interface. It exists so a script
// can keep the book up to date without driving the browser UI, and is mounted
// outside the same-origin guard: it authenticates with a bearer token rather
// than a cookie, so a forged cross-site request carries nothing useful.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// TokenEnv is the environment variable holding the API token. Without it the
// API stays off, which is what a household app running on a private network
// should default to.
const TokenEnv = "HB_API_TOKEN" //nolint:gosec // the name of the variable, not a token

// maxBodyBytes caps a request body. A batch of bookings is text, so this is
// generous without inviting a memory-exhaustion attempt.
const maxBodyBytes = 2 << 20 // 2 MiB

// Prefix is the path every route lives under.
const Prefix = "/api/v1/"

// sanitizeLog strips the characters that would let a request path forge a log
// line of its own.
var sanitizeLog = strings.NewReplacer("\n", "", "\r", "", "\t", " ")

// Server holds what the API handlers need.
type Server struct {
	store  *store.Store
	logger *slog.Logger
	token  string
}

// New creates an API server. An empty token disables every route, so an
// unconfigured deployment cannot be written to by accident.
func New(st *store.Store, logger *slog.Logger, token string) *Server {
	return &Server{store: st, logger: logger, token: strings.TrimSpace(token)}
}

// Enabled reports whether a token is configured.
func (s *Server) Enabled() bool { return s.token != "" }

// Handler builds the routes with authentication and logging in front of them.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/households", s.handleHouseholds)
	mux.HandleFunc("GET /api/v1/categories", s.handleCategories)
	mux.HandleFunc("POST /api/v1/categories", s.handleCreateCategory)
	mux.HandleFunc("GET /api/v1/members", s.handleMembers)
	mux.HandleFunc("GET /api/v1/tags", s.handleTags)
	mux.HandleFunc("GET /api/v1/report", s.handleReport)

	mux.HandleFunc("GET /api/v1/bookings", s.handleListBookings)
	mux.HandleFunc("POST /api/v1/bookings", s.handleCreateBooking)
	mux.HandleFunc("GET /api/v1/bookings/{id}", s.handleGetBooking)
	mux.HandleFunc("PUT /api/v1/bookings/{id}", s.handleUpdateBooking)
	mux.HandleFunc("DELETE /api/v1/bookings/{id}", s.handleDeleteBooking)

	return s.authenticate(mux)
}

// authenticate rejects anything without the configured token and logs the
// outcome. The token itself never reaches the log.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		switch {
		case !s.Enabled():
			writeError(rec, http.StatusServiceUnavailable, "API deaktiviert: kein Token konfiguriert")
		case !s.authorized(r):
			writeError(rec, http.StatusUnauthorized, "ungültiger oder fehlender API-Token")
		default:
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
			next.ServeHTTP(rec, r)
		}

		s.logger.Info("api request",
			"method", sanitizeLog.Replace(r.Method),
			"path", sanitizeLog.Replace(r.URL.Path),
			"status", rec.status,
			"durationMs", time.Since(start).Milliseconds(),
		)
	})
}

func (s *Server) authorized(r *http.Request) bool {
	return subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(s.token)) == 1
}

// bearerToken reads the token out of the Authorization header.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// statusRecorder remembers the status so the log line can report it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// ---- responses ----

// errorBody is the single shape every failure takes, so a caller only has to
// parse one thing to find out what went wrong.
type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

// fail turns a store error into the status it deserves.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "nicht gefunden")
	default:
		s.logger.Error("api failed",
			"path", sanitizeLog.Replace(r.URL.Path), "err", err)
		writeError(w, http.StatusInternalServerError, "interner Fehler")
	}
}

// decode reads a JSON body and refuses anything it does not know, so a
// misspelled field is reported instead of silently ignored.
func decode(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("mehr als ein JSON-Objekt im Body")
	}
	return nil
}

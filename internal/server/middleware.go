package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// maxRequestBody caps the size of request bodies. The application only accepts
// small HTML form submissions, so this prevents a single request from forcing
// the 32 MiB default form buffer to be allocated.
const maxRequestBody = 1 << 20 // 1 MiB

// contentSecurityPolicy is intentionally strict: everything is served from the
// binary itself, so no external origins are required. Inline styles are allowed
// because the templates use style attributes for bar widths and member colors;
// those values are sanitized by templ.
const contentSecurityPolicy = "default-src 'self'; " +
	"base-uri 'none'; " +
	"object-src 'none'; " +
	"frame-ancestors 'none'; " +
	"form-action 'self'; " +
	"img-src 'self' data:; " +
	"style-src 'self' 'unsafe-inline'; " +
	"script-src 'self'; " +
	"connect-src 'self'"

// securityHeaders sets conservative response headers for every request.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects state-changing requests that a browser reports as
// cross-site. The application has no authentication and relies on network
// isolation, so this is the cheapest effective defense against a malicious page
// silently posting to the instance (CSRF).
func (s *Server) sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		if !requestIsSameOrigin(r) {
			s.logger.Warn("cross-origin request rejected",
				"path", r.URL.Path, "origin", r.Header.Get("Origin"))
			http.Error(w, "Cross-Origin-Anfrage abgelehnt", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// requestIsSameOrigin reports whether r originates from the same site. It
// prefers the Fetch Metadata header and falls back to Origin. Requests from
// non-browser clients (neither header present) are allowed.
func requestIsSameOrigin(r *http.Request) bool {
	switch site := r.Header.Get("Sec-Fetch-Site"); site {
	case "same-origin", "same-site", "none":
		return true
	case "":
		// Fall through to the Origin check below.
	default:
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if i := strings.Index(origin, "://"); i >= 0 {
		origin = origin[i+3:]
	}
	return origin == r.Host
}

// limitBody caps the request body size of state-changing requests.
func limitBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && !isSafeMethod(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
		}
		next.ServeHTTP(w, r)
	})
}

// ---- request logging ----

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the underlying writer so that http.ResponseController and
// optimisations such as io.ReaderFrom keep working through this wrapper.
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start).String(),
		)
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				s.logger.Error("panic recovered", "err", rec, "path", r.URL.Path)
				http.Error(w, "Interner Serverfehler", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ---- gzip ----

var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

// compressible reports whether a Content-Type is worth compressing.
func compressible(contentType string) bool {
	ct, _, _ := strings.Cut(contentType, ";")
	ct = strings.TrimSpace(ct)
	switch ct {
	case "text/html", "text/css", "text/plain",
		"application/javascript", "text/javascript", "application/json":
		return true
	}
	return false
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true

	h := g.Header()
	if code == http.StatusNoContent || code == http.StatusNotModified ||
		h.Get("Content-Encoding") != "" || !compressible(h.Get("Content-Type")) {
		g.ResponseWriter.WriteHeader(code)
		return
	}

	h.Set("Content-Encoding", "gzip")
	h.Del("Content-Length")
	g.gz = gzipPool.Get().(*gzip.Writer)
	g.gz.Reset(g.ResponseWriter)
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if g.gz != nil {
		return g.gz.Write(p)
	}
	return g.ResponseWriter.Write(p)
}

func (g *gzipResponseWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// close finishes the gzip stream and returns the writer to the pool.
func (g *gzipResponseWriter) close() {
	if g.gz == nil {
		return
	}
	_ = g.gz.Close()
	g.gz.Reset(io.Discard)
	gzipPool.Put(g.gz)
	g.gz = nil
}

// Unwrap exposes the underlying writer for http.ResponseController.
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

// compressResponses transparently gzips textual responses for clients that
// advertise support. This noticeably reduces transfer size for the HTML pages
// and the embedded htmx bundle at a very small CPU cost.
func compressResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipResponseWriter{ResponseWriter: w}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

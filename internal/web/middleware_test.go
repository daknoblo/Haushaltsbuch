package web

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	return New(nil, discardLogger())
}

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	for header, want := range map[string]string{
		"Content-Security-Policy": contentSecurityPolicy,
		"X-Content-Type-Options":  "nosniff",
		"X-Frame-Options":         "DENY",
		"Referrer-Policy":         "no-referrer",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		headers    map[string]string
		wantStatus int
	}{
		{"GET is always allowed", http.MethodGet,
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusOK},
		{"same-origin POST", http.MethodPost,
			map[string]string{"Sec-Fetch-Site": "same-origin"}, http.StatusOK},
		{"cross-site POST", http.MethodPost,
			map[string]string{"Sec-Fetch-Site": "cross-site"}, http.StatusForbidden},
		{"matching origin", http.MethodPost,
			map[string]string{"Origin": "http://example.test"}, http.StatusOK},
		{"foreign origin", http.MethodPost,
			map[string]string{"Origin": "http://evil.test"}, http.StatusForbidden},
		{"no browser headers", http.MethodPost, nil, http.StatusOK},
	}

	s := testServer(t)
	h := s.sameOrigin(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://example.test/expenses/1", nil)
			r.Host = "example.test"
			for k, v := range tc.headers {
				r.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
		})
	}
}

func TestLimitBody(t *testing.T) {
	h := limitBody(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	body := strings.NewReader(strings.Repeat("x", maxRequestBody+1))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/expenses/1", body))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestCompressResponses(t *testing.T) {
	payload := strings.Repeat("Haushaltsbuch ", 200)
	h := compressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, payload)
	}))

	t.Run("gzip when accepted", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
			t.Fatalf("Content-Encoding = %q, want gzip", got)
		}
		if got := rec.Header().Get("Vary"); got != "Accept-Encoding" {
			t.Errorf("Vary = %q, want Accept-Encoding", got)
		}
		if rec.Body.Len() >= len(payload) {
			t.Errorf("compressed body (%d bytes) is not smaller than input (%d bytes)",
				rec.Body.Len(), len(payload))
		}
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatalf("gzip.NewReader: %v", err)
		}
		defer func() { _ = zr.Close() }()
		got, err := io.ReadAll(zr)
		if err != nil {
			t.Fatalf("read gzip body: %v", err)
		}
		if string(got) != payload {
			t.Error("decompressed body does not match the original payload")
		}
	})

	t.Run("plain when not accepted", func(t *testing.T) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want empty", got)
		}
		if rec.Body.String() != payload {
			t.Error("body was modified although gzip was not accepted")
		}
	})

	t.Run("binary content is not compressed", func(t *testing.T) {
		pdf := compressResponses(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("%PDF-1.7"))
		}))
		r := httptest.NewRequest(http.MethodGet, "/export/overview.pdf", nil)
		r.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		pdf.ServeHTTP(rec, r)

		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("Content-Encoding = %q, want empty", got)
		}
	})
}

func TestRateLimiterRefills(t *testing.T) {
	l := newRateLimiter()
	base := time.Now()

	for i := 0; i < rateBurst; i++ {
		if !l.allow("10.0.0.1", base) {
			t.Fatalf("request %d was rejected while the bucket should still hold tokens", i)
		}
	}
	if l.allow("10.0.0.1", base) {
		t.Error("bucket was not exhausted after the full burst")
	}

	// One token is regained per rateRefill.
	if !l.allow("10.0.0.1", base.Add(rateRefill)) {
		t.Error("bucket did not refill")
	}

	// Clients are tracked independently.
	if !l.allow("10.0.0.2", base) {
		t.Error("a second client was throttled by the first one's budget")
	}
}

func TestRateLimiterEvictsAndIsBounded(t *testing.T) {
	l := newRateLimiter()
	base := time.Now()

	l.allow("10.0.0.1", base)
	// A later request from a different client evicts the idle bucket.
	l.allow("10.0.0.2", base.Add(rateIdleTTL+time.Minute))

	l.mu.Lock()
	_, stillThere := l.buckets["10.0.0.1"]
	l.mu.Unlock()
	if stillThere {
		t.Error("idle bucket was not evicted")
	}

	// The map must not grow without bound when every client stays active.
	for i := 0; i < rateMaxBuckets+50; i++ {
		l.allow(fmt.Sprintf("10.1.%d.%d", i/256, i%256), base)
	}
	l.mu.Lock()
	size := len(l.buckets)
	l.mu.Unlock()
	if size > rateMaxBuckets {
		t.Errorf("limiter holds %d buckets, want at most %d", size, rateMaxBuckets)
	}
}

func TestOriginComparisonIgnoresCase(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	r.Host = "budget.example.test"
	r.Header.Set("Origin", "http://Budget.Example.Test")

	if !requestIsSameOrigin(r) {
		t.Error("a differently cased origin was treated as cross-site")
	}
}

func TestColorOrRejectsInvalidValues(t *testing.T) {
	const fallback = "#94a3b8"
	for _, in := range []string{"", "red", "javascript:alert(1)", "#12345", "#gggggg"} {
		if got := ColorOr(in); got != fallback {
			t.Errorf("ColorOr(%q) = %q, want the fallback", in, got)
		}
	}
	if got := ColorOr("#2563eb"); got != "#2563eb" {
		t.Errorf("ColorOr kept a valid color as %q", got)
	}
}

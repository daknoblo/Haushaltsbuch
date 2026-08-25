package i18n

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The standard requires every catalog to carry the same keys, so a missing
// translation is caught here instead of silently falling back at runtime.
func TestCatalogsHaveSameKeys(t *testing.T) {
	ref := catalogs[Default]
	for lang, c := range catalogs {
		if lang == Default {
			continue
		}
		for k := range ref {
			if _, ok := c[k]; !ok {
				t.Errorf("catalog %q is missing key %q", lang, k)
			}
		}
		for k := range c {
			if _, ok := ref[k]; !ok {
				t.Errorf("catalog %q has key %q that %q does not", lang, k, Default)
			}
		}
	}
}

// Format strings must keep the same verbs, otherwise Sprintf renders %!s(MISSING).
func TestCatalogsAgreeOnFormatVerbs(t *testing.T) {
	ref := catalogs[Default]
	for lang, c := range catalogs {
		if lang == Default {
			continue
		}
		for k, want := range ref {
			got, ok := c[k]
			if !ok {
				continue
			}
			if strings.Count(want, "%s") != strings.Count(got, "%s") {
				t.Errorf("key %q: %q has %d %%s, %q has %d",
					k, Default, strings.Count(want, "%s"), lang, strings.Count(got, "%s"))
			}
		}
	}
}

func TestTFallsBackToDefault(t *testing.T) {
	if got := T(English, "nav.overview"); got != "Overview" {
		t.Errorf("T(en) = %q", got)
	}
	if got := T(Lang("fr"), "nav.overview"); got != T(Default, "nav.overview") {
		t.Errorf("unknown language did not fall back: %q", got)
	}
	if got := T(German, "does.not.exist"); got != "does.not.exist" {
		t.Errorf("missing key should render as itself, got %q", got)
	}
}

func TestFromRequest(t *testing.T) {
	cases := map[string]Lang{
		"":                        Default,
		"de-DE,de;q=0.9":          German,
		"en-US,en;q=0.9":          English,
		"fr-FR,fr;q=0.9":          Default,
		"fr-FR,fr;q=0.9,en;q=0.8": English,
	}
	for header, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			r.Header.Set("Accept-Language", header)
		}
		if got := FromRequest(r); got != want {
			t.Errorf("FromRequest(%q) = %q, want %q", header, got, want)
		}
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := WithLang(context.Background(), English)
	if got := LangFrom(ctx); got != English {
		t.Errorf("LangFrom = %q", got)
	}
	if got := C(ctx, "nav.bookings"); got != "Bookings" {
		t.Errorf("C = %q", got)
	}
	if got := LangFrom(context.Background()); got != Default {
		t.Errorf("empty context = %q, want default", got)
	}
}

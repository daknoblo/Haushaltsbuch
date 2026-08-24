// Package i18n holds the user-facing text catalogs. Everything shown in the UI
// or returned as an HTTP error message is looked up here, so no German string
// ends up in Go code or templates.
package i18n

import (
	"net/http"
	"sort"
	"strings"
)

// Lang identifies a supported catalog.
type Lang string

const (
	// German is the default because the application is used in Germany.
	German Lang = "de"
	// English is the fallback for browsers that ask for it.
	English Lang = "en"
)

// Default is used when a request carries no usable language preference.
const Default = German

// Key identifies a translatable string.
type Key string

// Catalog maps keys to translated strings.
type Catalog map[Key]string

var catalogs = map[Lang]Catalog{
	German:  german,
	English: english,
}

// Langs returns the supported languages in a stable order.
func Langs() []Lang {
	out := make([]Lang, 0, len(catalogs))
	for l := range catalogs {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Catalogs exposes the raw catalogs, used by the consistency test.
func Catalogs() map[Lang]Catalog { return catalogs }

// Supported reports whether a language has a catalog.
func Supported(l Lang) bool {
	_, ok := catalogs[l]
	return ok
}

// T returns the translation of key in lang, falling back to the default
// catalog and finally to the key itself so nothing renders empty.
func T(lang Lang, key Key) string {
	if c, ok := catalogs[lang]; ok {
		if s, ok := c[key]; ok {
			return s
		}
	}
	if s, ok := catalogs[Default][key]; ok {
		return s
	}
	return string(key)
}

// FromRequest picks the language from the Accept-Language header. Only the
// primary tag is considered; the app has no language switcher.
func FromRequest(r *http.Request) Lang {
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		primary, _, _ := strings.Cut(tag, "-")
		if l := Lang(strings.ToLower(primary)); Supported(l) {
			return l
		}
	}
	return Default
}

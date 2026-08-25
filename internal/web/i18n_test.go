package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/daknoblo/Haushaltsbuch/internal/i18n"
)

// A key no catalog knows renders as the key itself, which is how "nav.income"
// once ended up as a table heading. Keys are string literals the compiler
// cannot check, so they are matched against the catalog here.
var keyUses = []*regexp.Regexp{
	regexp.MustCompile(`\bTf?\(ctx, "([^"]+)"`),
	regexp.MustCompile(`\bclientError\([^)]*, "([^"]+)"\)`),
}

func TestTranslationKeysExist(t *testing.T) {
	templates, err := filepath.Glob("*.templ")
	if err != nil {
		t.Fatalf("glob templates: %v", err)
	}
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob sources: %v", err)
	}

	catalog := i18n.Catalogs()[i18n.Default]
	var checked int
	for _, path := range append(templates, sources...) {
		// The generated output only repeats what the template already declares.
		if strings.HasSuffix(path, "_templ.go") {
			continue
		}
		content, err := os.ReadFile(path) // #nosec G304 -- paths come from a fixed glob in the package directory
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		for _, re := range keyUses {
			for _, match := range re.FindAllStringSubmatch(string(content), -1) {
				checked++
				if _, ok := catalog[i18n.Key(match[1])]; !ok {
					t.Errorf("%s uses %q, which no catalog defines", path, match[1])
				}
			}
		}
	}

	// A pattern that stops matching would let the check pass without doing
	// anything, so the absence of findings is a failure in itself.
	if checked < len(catalog)/2 {
		t.Fatalf("only %d key uses found, the patterns no longer match", checked)
	}
}

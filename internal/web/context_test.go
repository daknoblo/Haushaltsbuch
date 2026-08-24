package web

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestCleanName(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"  Miete  ", "Miete"},
		{"", ""},
		{"   ", ""},
		{strings.Repeat("ä", maxNameLen+10), strings.Repeat("ä", maxNameLen)},
	}
	for _, tc := range tests {
		if got := cleanName(tc.in); got != tc.want {
			t.Errorf("cleanName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCleanColor(t *testing.T) {
	tests := map[string]string{
		"#2563EB":                        "#2563eb",
		"  #059669 ":                     "#059669",
		"red":                            "",
		"#12345":                         "",
		"#2563eb; background:url(x)":     "",
		"javascript:alert(1)":            "",
		"":                               "",
		"#2563eb\"onload=\"alert(1)":     "",
		"#gggggg":                        "",
		"#2563eb#2563eb#2563eb#2563eb#0": "",
	}
	for in, want := range tests {
		if got := cleanColor(in); got != want {
			t.Errorf("cleanColor(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanMonthAndDate(t *testing.T) {
	if got := cleanMonth("2026-07"); got != "2026-07" {
		t.Errorf("cleanMonth valid = %q", got)
	}
	for _, in := range []string{"", "2026-13", "2026", "not-a-month", "2026-07-01"} {
		if got := cleanMonth(in); got != "" {
			t.Errorf("cleanMonth(%q) = %q, want empty", in, got)
		}
	}

	if got := cleanDate("2026-07-26"); got != "2026-07-26" {
		t.Errorf("cleanDate valid = %q", got)
	}
	for _, in := range []string{"", "2026-07", "26.07.2026", "2026-02-30"} {
		if got := cleanDate(in); got != "" {
			t.Errorf("cleanDate(%q) = %q, want empty", in, got)
		}
	}
}

func TestParseID(t *testing.T) {
	tests := map[string]int64{
		"42":  42,
		"0":   0,
		"-1":  0,
		"abc": 0,
		"":    0,
		"1e3": 0,
	}
	for in, want := range tests {
		if got := parseID(in); got != want {
			t.Errorf("parseID(%q) = %d, want %d", in, got, want)
		}
	}
}

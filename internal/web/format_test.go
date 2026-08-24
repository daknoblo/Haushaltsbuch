package web

import (
	"errors"
	"math"
	"testing"
)

func TestParseCents(t *testing.T) {
	cases := []struct {
		in   string
		want int64
	}{
		{"", 0},
		{"1234.56", 123456},
		{"1.234,56", 123456},
		{"1234,56", 123456},
		{"1.234,56 €", 123456},
		{"-12,50", -1250},
		{"12.", 1200},
	}
	for _, c := range cases {
		got, err := ParseCents(c.in)
		if err != nil {
			t.Errorf("ParseCents(%q) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseCents(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestParseCentsRejectsNonFinite(t *testing.T) {
	// strconv.ParseFloat accepts these; converting them to int64 would saturate.
	for _, in := range []string{"NaN", "Inf", "-Inf", "1e30", "-1e30"} {
		if _, err := ParseCents(in); !errors.Is(err, ErrAmountRange) {
			t.Errorf("ParseCents(%q) error = %v, want ErrAmountRange", in, err)
		}
	}
}

func TestParseFloatLooseRejectsNonFinite(t *testing.T) {
	for _, in := range []string{"NaN", "Inf"} {
		if _, err := ParseFloatLoose(in); !errors.Is(err, ErrAmountRange) {
			t.Errorf("ParseFloatLoose(%q) error = %v, want ErrAmountRange", in, err)
		}
	}
}

func TestFormatCentsHandlesExtremes(t *testing.T) {
	// Negating math.MinInt64 overflows back to itself and used to produce a
	// string with two minus signs.
	for _, in := range []int64{math.MinInt64, math.MaxInt64} {
		got := FormatCents(in)
		if got[:2] == "--" {
			t.Errorf("FormatCents(%d) = %q", in, got)
		}
	}
	if got := FormatCents(123456); got != "1.234,56" {
		t.Errorf("FormatCents(123456) = %q, want %q", got, "1.234,56")
	}
	if got := FormatCents(-5); got != "-0,05" {
		t.Errorf("FormatCents(-5) = %q, want %q", got, "-0,05")
	}
}

func TestAssetURLCarriesVersion(t *testing.T) {
	n := Nav{Version: "v20260816-1200"}
	if got := n.AssetURL("app.css"); got != "/static/app.css?v=v20260816-1200" {
		t.Errorf("AssetURL = %q", got)
	}
	if got := (Nav{}).AssetURL("app.js"); got != "/static/app.js?v=dev" {
		t.Errorf("AssetURL without version = %q", got)
	}
}

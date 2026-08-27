package web

import (
	"math"
	"testing"
)

// A household figure is typed by hand and then never checked against a bank, so
// the parser is the only thing standing between a typo and a wrong plan. These
// properties hold for every input, not just the ones somebody thought to write
// down as a table test.
func FuzzParseCents(f *testing.F) {
	for _, seed := range []string{
		"", " ", "0", "1234,56", "1.234,56", "1234.56", "-99,99", "12 €", "€",
		"1.234.567,89", ",5", "5,", "1e3", "NaN", "+Inf", "-Inf", "0x1p10",
		"1,2,3", "1.2.3", "--1", "1_000", "٣", "1,555", "9999999999999999999",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, in string) {
		cents, err := ParseCents(in)
		if err != nil {
			// A rejected value has to be reported as zero, because a caller that
			// ignores the error must not end up booking a leftover figure.
			if cents != 0 {
				t.Fatalf("ParseCents(%q) failed with err=%v but still returned %d", in, err, cents)
			}
			return
		}

		// Package calc adds these up as int64. A value that slipped past the
		// bound would make a total overflow rather than merely look wrong.
		if cents > MaxAmountCents || cents < -MaxAmountCents {
			t.Fatalf("ParseCents(%q) accepted %d, beyond ±%d", in, cents, MaxAmountCents)
		}

		// What the parser accepted, the formatter has to render, and the parser
		// has to read back unchanged. A mismatch here is how an amount quietly
		// changes between the form and the list showing it.
		again, err := ParseCents(FormatCents(cents))
		if err != nil {
			t.Fatalf("ParseCents(%q) = %d, but FormatCents(%d) = %q does not parse: %v",
				in, cents, cents, FormatCents(cents), err)
		}
		if again != cents {
			t.Fatalf("round trip changed the amount: %q -> %d -> %q -> %d",
				in, cents, FormatCents(cents), again)
		}
	})
}

// The looser parser feeds the split percentages, where an infinity or a NaN
// would spread across every member's share instead of being caught at entry.
func FuzzParseFloatLoose(f *testing.F) {
	for _, seed := range []string{
		"", "50", "33,33", "33.33", "-1", "1e309", "NaN", "Inf", "0,0000001", "١٢",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, in string) {
		v, err := ParseFloatLoose(in)
		if err != nil {
			if v != 0 {
				t.Fatalf("ParseFloatLoose(%q) failed with err=%v but returned %v", in, err, v)
			}
			return
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("ParseFloatLoose(%q) returned %v", in, v)
		}
	})
}

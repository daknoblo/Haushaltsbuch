package web

import "testing"

// A category written through the API may carry no icon at all, so the guess has
// to happen where it is drawn. Getting this wrong showed a bare dot next to
// every category a script had created.
func TestAnIconIsAlwaysFound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		icon, name, want string
	}{
		{"", "Restaurant", "utensils"},
		{"", "Gesundheit", "heart"},
		{"", "Shopping", "cart"},
		{"", "Miete", "home"},
		{"", "Völlig Unbekanntes", iconFallback},
		{"piggy", "Restaurant", "piggy"},
		{"gibtsnicht", "Restaurant", "utensils"},
	}
	for _, c := range cases {
		if got := IconOr(c.icon, c.name); got != c.want {
			t.Errorf("IconOr(%q, %q) = %q, want %q", c.icon, c.name, got, c.want)
		}
	}
}

// "Haushalt" contains "haus", so without its own entry it would wear the same
// symbol as the rent and the two rows would be told apart by their label only.
func TestHouseholdGoodsDoNotLookLikeRent(t *testing.T) {
	t.Parallel()

	if got := GuessIcon("Haushalt"); got == GuessIcon("Miete") {
		t.Errorf("Haushalt and Miete share the icon %q", got)
	}
}

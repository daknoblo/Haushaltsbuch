package web

import (
	"context"
	"net/url"
)

// Every card that reads a stretch of time carries its own picker, because the
// questions they answer are not asked over the same span: what a person owes is
// a question about one month, while a savings rate over one month says little.
const (
	secSettle = "settle"
	secShare  = "share"
	secSave   = "save"
	secFix    = "fix"
	secRule   = "rule"
	secFlow   = "flow"
	secCat    = "cat"
)

var sectionKeys = []string{secSettle, secShare, secSave, secFix, secRule, secFlow, secCat}

// SectionVM is one card's period picker plus the span it resolved to.
type SectionVM struct {
	Key     string
	Value   string
	Label   string
	Months  []string
	Options []PeriodOption
}

// sectionParam is the query key a card's period hides behind.
func sectionParam(key string) string { return "s_" + key }

// sectionSpan reads one card's period, falling back to the page's own. A card
// the reader never touched therefore follows the selector at the top.
func sectionSpan(q url.Values, key, fallback string) string {
	v := q.Get(sectionParam(key))
	if ValidMonth(v) {
		return NormalizeMonth(v)
	}
	if _, ok := periodMonths[v]; ok {
		return v
	}
	return fallback
}

// sectionMonths turns a card's period into the months it covers. A value that
// names a month is exactly that month; anything else is a span around the
// anchor and follows the same rules as the selector at the top.
func sectionMonths(value, anchor string) []string {
	if ValidMonth(value) {
		return []string{NormalizeMonth(value)}
	}
	return rangeMonths(value, anchor)
}

// sectionOptions lists what a card can be switched to: every month of the
// anchor's year, then the spans. "1m" and "2m" are left out — a single month is
// better named than counted, and two months answer no question anyone asks.
func sectionOptions(ctx context.Context, q url.Values, key, anchor, active string) []PeriodOption {
	var out []PeriodOption
	add := func(value, label string) {
		out = append(out, PeriodOption{
			Key:    value,
			Label:  label,
			Active: value == active,
			URL:    dashboardSwap(q, sectionParam(key), value),
		})
	}
	for _, m := range calendarYear(anchor) {
		add(m, MonthLabel(ctx, m))
	}
	for _, p := range []struct{ key, label string }{
		{periodQuarter, "dash.rangeQuarter"},
		{"6m", "dash.rangeHalf"},
		{periodYear, "dash.rangeYear"},
	} {
		add(p.key, T(ctx, p.label))
	}
	return out
}

// sectionLabel names the chosen span for the closed picker.
func sectionLabel(ctx context.Context, value, anchor string) string {
	if ValidMonth(value) {
		return MonthLabel(ctx, value)
	}
	for _, p := range periodOrder {
		if p.key == value {
			return T(ctx, p.label)
		}
	}
	return rangeLabel(ctx, sectionMonths(value, anchor))
}

// dashboardSwap returns the dashboard URL with one parameter replaced and every
// other choice on the page left alone, so switching one card does not reset the
// six beside it.
func dashboardSwap(q url.Values, key, value string) string {
	next := url.Values{}
	for k, vs := range q {
		next[k] = append([]string(nil), vs...)
	}
	next.Set(key, value)
	return "/dashboard?" + next.Encode()
}

// sectionQuery is the part of a link that carries the cards' own periods, so
// the controls at the top of the page keep them when they rebuild their URLs.
func sectionQuery(q url.Values) string {
	out := url.Values{}
	for _, k := range sectionKeys {
		if v := q.Get(sectionParam(k)); v != "" {
			out.Set(sectionParam(k), v)
		}
	}
	if len(out) == 0 {
		return ""
	}
	return "&" + out.Encode()
}

package web

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// BookingSearch holds one request-local index shared by server and browser.
type BookingSearch struct {
	Text        string
	Suggestions []string
}

// SuggestionsJSON safely transports completion values in a data attribute.
func (s BookingSearch) SuggestionsJSON() (string, error) {
	value, err := json.Marshal(s.Suggestions)
	return string(value), err
}

func bookingSearch(ctx context.Context, row BookingRow, tags map[int64]store.Tag) BookingSearch {
	b := row.Booking
	values := make([]string, 0, 64)
	suggestions := make([]string, 0, 16)
	seen := make(map[string]bool)
	add := func(value string, suggest bool) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		values = append(values, value)
		if suggest && !seen[value] {
			suggestions = append(suggestions, value)
			seen[value] = true
		}
	}
	money := func(cents int64) {
		add(FormatEUR(cents), false)
		add(formatDecimal(cents), false)
		add(strings.ReplaceAll(formatDecimal(cents), ".", ","), false)
		add(strconv.FormatInt(cents, 10), false)
	}
	date := func(value string) {
		if value != "" {
			add(value, false)
			add(FormatDate(value), false)
			if len(value) >= 7 {
				add(MonthLabel(ctx, value[:7]), false)
			}
		}
	}

	add(b.Name, true)
	add(b.Note, false)
	add(b.ExternalID, true)
	add(strconv.FormatInt(b.ID, 10), false)
	add(row.Category.Name, true)
	add(row.Category.Color, false)
	add(row.Category.Icon, false)
	add(row.Payer.Name, true)
	add(row.Payer.Color, false)
	add(string(b.Direction), true)
	add(DirectionLabel(ctx, b.Direction), true)
	add(string(b.Frequency), true)
	add(RhythmLabel(ctx, b), true)
	add(strconv.Itoa(b.Interval), false)
	add(string(b.SplitMode), true)
	add(SplitModeLabel(ctx, b.SplitMode), true)
	date(b.StartsOn)
	date(b.EndsOn)
	add(row.DateLabel(ctx), false)
	add(b.CreatedAt, false)
	add(b.UpdatedAt, false)
	money(b.AmountCents)
	money(row.AmountCents())
	money(row.MonthlyCents())
	if b.Frequency.Recurring() {
		add(string(b.DuePoint), true)
		add(DuePointLabel(ctx, b.DuePoint), true)
	}
	if !row.IsIncome() {
		add(string(b.CostNature), true)
		add(CostNatureLabel(ctx, b.CostNature), true)
		add(string(b.BudgetClass), true)
		add(BudgetClassLabel(ctx, b.BudgetClass), true)
		if row.SettlementEnabled() {
			add(T(ctx, "bookings.settle"), true)
		} else {
			add(T(ctx, "bookings.noSettle"), true)
		}
	}
	if row.IsShared() {
		add(T(ctx, "bookings.shared"), true)
	} else if len(row.Carriers) == 1 {
		add(T(ctx, "bookings.alone"), true)
	}
	for _, id := range row.TagIDs {
		add(tags[id].Name, true)
		add(tags[id].Color, false)
	}
	for _, split := range row.Splits {
		for _, member := range row.Members {
			if member.ID == split.MemberID {
				add(member.Name, true)
				add(member.Color, false)
				break
			}
		}
		switch b.SplitMode {
		case store.SplitPercent:
			value := strconv.FormatFloat(split.Value, 'f', -1, 64)
			add(value+" %", false)
			add(strings.ReplaceAll(value, ".", ",")+" %", false)
		case store.SplitFixed:
			money(int64(split.Value))
		}
	}
	if b.Frequency.Recurring() {
		for _, override := range row.Overrides {
			money(override.AmountCents)
			date(override.StartsOn)
			date(override.EndsOn)
			add(override.Note, false)
		}
	}
	return BookingSearch{Text: strings.ToLower(strings.Join(values, "\n")), Suggestions: suggestions}
}

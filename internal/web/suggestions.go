package web

import (
	"strings"

	"github.com/daknoblo/Haushaltsbuch/internal/store"
)

// extraCategories rounds out the seeded set. Together they cover what a German
// household usually books; the settings offer whatever is still missing so a
// category list can be filled in without inventing names.
var extraCategories = []store.SeedCategory{
	{Name: "Gas", Class: store.DirExpense, Color: "#fb923c", Icon: "flame"},
	{Name: "Wasser", Class: store.DirExpense, Color: "#38bdf8", Icon: "droplet"},
	{Name: "Telefon", Class: store.DirExpense, Color: "#c084fc", Icon: "phone"},
	{Name: "Öffentliche Verkehrsmittel", Class: store.DirExpense, Color: "#fb7185", Icon: "bus"},
	{Name: "Restaurant", Class: store.DirExpense, Color: "#f43f5e", Icon: "utensils"},
	{Name: "Gesundheit", Class: store.DirExpense, Color: "#e11d48", Icon: "heart"},
	{Name: "Kleidung", Class: store.DirExpense, Color: "#d946ef", Icon: "shirt"},
	{Name: "Sport", Class: store.DirExpense, Color: "#22c55e", Icon: "dumbbell"},
	{Name: "Bildung", Class: store.DirExpense, Color: "#0284c7", Icon: "school"},
	{Name: "Geschenke", Class: store.DirExpense, Color: "#f472b6", Icon: "gift"},
	{Name: "Urlaub", Class: store.DirExpense, Color: "#06b6d4", Icon: "plane"},
	{Name: "Haustier", Class: store.DirExpense, Color: "#a16207", Icon: "paw"},
	{Name: "Kinder", Class: store.DirExpense, Color: "#facc15", Icon: "baby"},
	{Name: "Reparaturen", Class: store.DirExpense, Color: "#78716c", Icon: "tool"},
	{Name: "Kredit", Class: store.DirExpense, Color: "#dc2626", Icon: "card"},
	{Name: "Kapitalerträge", Class: store.DirIncome, Color: "#059669", Icon: "chart"},
}

// suggestCategories returns the proposals a household has not created yet,
// matched by name so a renamed category does not come back as a suggestion.
func suggestCategories(existing []store.Category) []store.SeedCategory {
	taken := make(map[string]bool, len(existing))
	for _, c := range existing {
		taken[strings.ToLower(strings.TrimSpace(c.Name))] = true
	}

	all := make([]store.SeedCategory, 0, len(store.DefaultCategories)+len(extraCategories))
	all = append(all, store.DefaultCategories...)
	all = append(all, extraCategories...)

	out := make([]store.SeedCategory, 0, len(all))
	for _, c := range all {
		if !taken[strings.ToLower(c.Name)] {
			out = append(out, c)
		}
	}
	return out
}

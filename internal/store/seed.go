package store

import "context"

// SeedCategory is a category proposed for a household. The same list seeds a
// new household and drives the suggestions shown in the settings.
type SeedCategory struct {
	Name  string
	Class Direction
	Color string
	Icon  string
}

// DefaultCategories are created with every new household. Colors are reused by
// the breakdowns and the Sankey diagram, so every category carries one.
var DefaultCategories = []SeedCategory{
	{"Gehalt", DirIncome, "#10b981", "wallet"},
	{"Sonstige Einnahmen", DirIncome, "#34d399", "coins"},
	{"Miete", DirExpense, "#6366f1", "home"},
	{"Nebenkosten", DirExpense, "#818cf8", "droplet"},
	{"Strom", DirExpense, "#a78bfa", "bolt"},
	{"Internet", DirExpense, "#8b5cf6", "wifi"},
	{"Versicherung", DirExpense, "#f59e0b", "shield"},
	{"Lebensmittel", DirExpense, "#ef4444", "cart"},
	{"Mobilität", DirExpense, "#f97316", "car"},
	{"Abo", DirExpense, "#ec4899", "play"},
	{"Freizeit", DirExpense, "#14b8a6", "ticket"},
	{"Sparrate", DirExpense, "#0ea5e9", "piggy"},
	{"Sonstiges", DirExpense, "#94a3b8", "tag"},
}

// MemberColors is the palette every member is colored from, in the order they
// are handed out. It lives here because the seeded household draws from the
// same list as one filled in by hand — two lists would drift apart.
var MemberColors = []string{"#2563eb", "#db2777", "#059669", "#d97706", "#7c3aed", "#0891b2"}

// MemberColor returns the color for the n-th member of a household.
func MemberColor(n int) string {
	return MemberColors[n%len(MemberColors)]
}

// CreateHouseholdSeeded creates a household pre-populated with one member and
// the default categories, so it is immediately usable. Either the whole
// household is created or none of it.
func (s *Store) CreateHouseholdSeeded(ctx context.Context, name string) (Household, error) {
	var out Household
	err := s.withTx(ctx, func(tx *Store) error {
		h, err := tx.CreateHousehold(ctx, name)
		if err != nil {
			return err
		}
		if _, err := tx.CreateMember(ctx, h.ID, "Ich", MemberColor(0)); err != nil {
			return err
		}
		for _, c := range DefaultCategories {
			cat := Category{Name: c.Name, Classification: c.Class, Color: c.Color, Icon: c.Icon}
			if _, err := tx.CreateCategory(ctx, h.ID, cat); err != nil {
				return err
			}
		}
		out = h
		return nil
	})
	if err != nil {
		return Household{}, err
	}
	return out, nil
}

// EnsureSeed creates a default household when the database is empty and
// guarantees that an active household is selected.
func (s *Store) EnsureSeed(ctx context.Context) error {
	return s.withTx(ctx, func(tx *Store) error {
		n, err := tx.CountHouseholds(ctx)
		if err != nil {
			return err
		}

		if n == 0 {
			h, err := tx.CreateHouseholdSeeded(ctx, "Mein Haushalt")
			if err != nil {
				return err
			}
			if _, err := tx.CreateMember(ctx, h.ID, "Partner/in", MemberColor(1)); err != nil {
				return err
			}
			return tx.SetActiveHousehold(ctx, h.ID)
		}

		active, err := tx.ActiveHouseholdID(ctx)
		if err != nil {
			return err
		}
		if active == 0 {
			hs, err := tx.ListHouseholds(ctx)
			if err != nil {
				return err
			}
			if len(hs) > 0 {
				return tx.SetActiveHousehold(ctx, hs[0].ID)
			}
		}
		return nil
	})
}

package store

import "context"

var (
	defaultSections   = []string{"Wohnen", "Versicherungen", "Lebenshaltung", "Freizeit", "Sparen"}
	defaultCategories = []string{"Miete", "Strom", "Versicherung", "Lebensmittel", "Abo", "Sparrate"}
)

// CreateHouseholdSeeded creates a household pre-populated with one member and
// the default sections and categories, so it is immediately usable. Either the
// whole household is created or none of it.
func (s *Store) CreateHouseholdSeeded(ctx context.Context, name string) (Household, error) {
	var out Household
	err := s.withTx(ctx, func(tx *Store) error {
		h, err := tx.CreateHousehold(ctx, name)
		if err != nil {
			return err
		}
		if _, err := tx.CreateMember(ctx, h.ID, "Ich", "#2563eb"); err != nil {
			return err
		}
		for _, n := range defaultSections {
			if _, err := tx.CreateSection(ctx, h.ID, n); err != nil {
				return err
			}
		}
		for _, n := range defaultCategories {
			if _, err := tx.CreateCategory(ctx, h.ID, n); err != nil {
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

// EnsureSeed creates a default household (with members, sections and
// categories) when the database is empty and guarantees that an active
// household is selected.
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
			if _, err := tx.CreateMember(ctx, h.ID, "Partner/in", "#db2777"); err != nil {
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

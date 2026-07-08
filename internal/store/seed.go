package store

var (
	defaultSections   = []string{"Wohnen", "Versicherungen", "Lebenshaltung", "Freizeit", "Sparen"}
	defaultCategories = []string{"Miete", "Strom", "Versicherung", "Lebensmittel", "Abo", "Sparrate"}
)

// CreateHouseholdSeeded creates a household pre-populated with one member and
// the default sections and categories, so it is immediately usable.
func (s *Store) CreateHouseholdSeeded(name string) (Household, error) {
	h, err := s.CreateHousehold(name)
	if err != nil {
		return Household{}, err
	}
	if _, err := s.CreateMember(h.ID, "Ich", "#2563eb"); err != nil {
		return Household{}, err
	}
	for _, n := range defaultSections {
		if _, err := s.CreateSection(h.ID, n); err != nil {
			return Household{}, err
		}
	}
	for _, n := range defaultCategories {
		if _, err := s.CreateCategory(h.ID, n); err != nil {
			return Household{}, err
		}
	}
	return h, nil
}

// EnsureSeed creates a default household (with members, sections and
// categories) when the database is empty and guarantees that an active
// household is selected.
func (s *Store) EnsureSeed() error {
	n, err := s.CountHouseholds()
	if err != nil {
		return err
	}

	if n == 0 {
		h, err := s.CreateHouseholdSeeded("Mein Haushalt")
		if err != nil {
			return err
		}
		if _, err := s.CreateMember(h.ID, "Partner/in", "#db2777"); err != nil {
			return err
		}
		return s.SetActiveHousehold(h.ID)
	}

	active, err := s.ActiveHouseholdID()
	if err != nil {
		return err
	}
	if active == 0 {
		hs, err := s.ListHouseholds()
		if err != nil {
			return err
		}
		if len(hs) > 0 {
			return s.SetActiveHousehold(hs[0].ID)
		}
	}
	return nil
}

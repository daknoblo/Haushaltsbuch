package store

import (
	"fmt"
	"time"
)

// ValidateDateRange accepts ISO calendar dates and empty, open bounds.
// Invalid dates and an end before the start wrap ErrInvalid.
func ValidateDateRange(startsOn, endsOn string) error {
	for _, date := range []string{startsOn, endsOn} {
		if date == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", date); err != nil {
			return fmt.Errorf("%w: %q is not a date", ErrInvalid, date)
		}
	}
	if startsOn != "" && endsOn != "" && endsOn < startsOn {
		return fmt.Errorf("%w: the end precedes the start", ErrInvalid)
	}
	return nil
}

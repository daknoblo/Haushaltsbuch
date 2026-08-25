package i18n

// monthNames holds the month names of one language, indexed by month-1. They
// sit next to the catalogs rather than inside them, because twelve numbered
// keys per length would drown the actual UI strings.
type monthNames struct {
	long  [12]string
	short [12]string
}

var months = map[Lang]monthNames{
	German:  germanMonths,
	English: englishMonths,
}

// MonthName returns the full name of month m (1-12) in lang.
func MonthName(lang Lang, m int) string {
	return monthOf(lang, m, func(n monthNames) [12]string { return n.long })
}

// MonthAbbr returns the abbreviated name of month m (1-12) in lang.
func MonthAbbr(lang Lang, m int) string {
	return monthOf(lang, m, func(n monthNames) [12]string { return n.short })
}

func monthOf(lang Lang, m int, pick func(monthNames) [12]string) string {
	if m < 1 || m > 12 {
		return ""
	}
	n, ok := months[lang]
	if !ok {
		n = months[Default]
	}
	return pick(n)[m-1]
}

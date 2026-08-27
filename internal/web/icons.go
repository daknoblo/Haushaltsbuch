package web

import (
	"sort"
	"strings"

	"github.com/a-h/templ"
)

// Icons are drawn as inline SVG rather than loaded from a font or a sprite
// service, because the Content-Security-Policy grants no external origin and a
// dozen glyphs are not worth a dependency. Every path is stroked with
// currentColor inside a 24x24 box.
const (
	iconStroke = `stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" fill="none"`

	iconFallback = "tag"
)

// iconPaths maps an icon key to the body of its SVG.
var iconPaths = map[string]string{
	"home":     `<path d="M3.5 10.5 12 4l8.5 6.5V20a1 1 0 0 1-1 1h-4v-6h-7v6h-4a1 1 0 0 1-1-1z"/>`,
	"bolt":     `<path d="M13.5 3 5 13.5h5.5L10 21l8.5-10.5H13z"/>`,
	"flame":    `<path d="M12 21c3.3 0 6-2.4 6-5.5 0-4.5-6-12-6-12S6 11 6 15.5C6 18.6 8.7 21 12 21z"/><path d="M12 21c1.5 0 2.7-1.1 2.7-2.6 0-2-2.7-4.7-2.7-4.7s-2.7 2.7-2.7 4.7C9.3 19.9 10.5 21 12 21z"/>`,
	"droplet":  `<path d="M12 3c3 4 6 7.2 6 10.5A6 6 0 0 1 6 13.5C6 10.2 9 7 12 3z"/>`,
	"wifi":     `<path d="M2.5 9a15 15 0 0 1 19 0"/><path d="M5.5 12.5a10.5 10.5 0 0 1 13 0"/><path d="M8.5 16a6 6 0 0 1 7 0"/><path d="M12 19.5h.01"/>`,
	"phone":    `<rect x="6.5" y="2.5" width="11" height="19" rx="2.5"/><path d="M10.5 18.5h3"/>`,
	"car":      `<path d="M4 16v2.5h3V16"/><path d="M17 16v2.5h3V16"/><path d="M3 16v-4l2-5h14l2 5v4z"/><path d="M6.5 12.5h2M15.5 12.5h2"/>`,
	"bus":      `<rect x="4" y="3.5" width="16" height="14" rx="2"/><path d="M4 11h16"/><path d="M7 20.5v-3M17 20.5v-3"/><path d="M7.5 14.5h.01M16.5 14.5h.01"/>`,
	"cart":     `<path d="M2.5 3.5h2l2.5 11h11"/><path d="M6 7.5h15l-2 6H7"/><path d="M9 19.5h.01M17 19.5h.01"/>`,
	"utensils": `<path d="M6 2.5v8a2.5 2.5 0 0 0 5 0v-8"/><path d="M8.5 10.5v11"/><path d="M17.5 2.5c-1.5 1.5-2 3.5-2 6s.7 3 2 3v10"/>`,
	"shield":   `<path d="M12 2.5 20 5.5v6c0 4.7-3.2 8.6-8 10-4.8-1.4-8-5.3-8-10v-6z"/>`,
	"heart":    `<path d="M12 20.5C6.5 17 3.5 13.7 3.5 10.2A4.2 4.2 0 0 1 12 8a4.2 4.2 0 0 1 8.5 2.2c0 3.5-3 6.8-8.5 10.3z"/>`,
	"pill":     `<rect x="2.8" y="8.5" width="18.4" height="7" rx="3.5" transform="rotate(-45 12 12)"/><path d="M8.5 8.5 15.5 15.5"/>`,
	"school":   `<path d="M2.5 8.5 12 4l9.5 4.5L12 13z"/><path d="M6.5 10.7V16c0 1.4 2.5 2.5 5.5 2.5s5.5-1.1 5.5-2.5v-5.3"/><path d="M21.5 8.5v5"/>`,
	"gift":     `<rect x="3" y="9" width="18" height="4"/><path d="M4.5 13v7.5h15V13"/><path d="M12 9v11.5"/><path d="M12 9C10 9 7.5 8.6 7.5 6.4 7.5 4.9 8.6 4 9.8 4c1.6 0 2.2 2.4 2.2 5zM12 9c2 0 4.5-.4 4.5-2.6 0-1.5-1.1-2.4-2.3-2.4C12.6 4 12 6.4 12 9z"/>`,
	"plane":    `<path d="M10.5 21 12 16l8.5-2.5a1.5 1.5 0 0 0 0-3L12 8l-1.5-5H8.5l1 5-4.5 1.3-1.7-2.3H2l1.2 4-1.2 4h1.3l1.7-2.3L9.5 16l-1 5z"/>`,
	"film":     `<rect x="2.5" y="4.5" width="19" height="15" rx="2"/><path d="M7 4.5v15M17 4.5v15"/><path d="M2.5 12h19"/>`,
	"play":     `<circle cx="12" cy="12" r="9"/><path d="M10 8.5 16 12l-6 3.5z"/>`,
	"ticket":   `<path d="M3 8.5V6.5a1 1 0 0 1 1-1h16a1 1 0 0 1 1 1v2a3.5 3.5 0 0 0 0 7v2a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1v-2a3.5 3.5 0 0 0 0-7z"/><path d="M13.5 5.5v13"/>`,
	"shirt":    `<path d="M8.5 3 12 5.5 15.5 3 21 6l-2.5 4-2 -1v9h-9v-9l-2 1L3 6z"/>`,
	"paw":      `<circle cx="7" cy="8" r="2"/><circle cx="12" cy="6" r="2"/><circle cx="17" cy="8" r="2"/><path d="M12 11c-3 0-5.5 2.6-5.5 5.3 0 1.8 1.3 2.9 3 2.9 1 0 1.7-.5 2.5-.5s1.5.5 2.5.5c1.7 0 3-1.1 3-2.9C17.5 13.6 15 11 12 11z"/>`,
	"baby":     `<circle cx="12" cy="9" r="5.5"/><path d="M9.8 8h.01M14.2 8h.01"/><path d="M10 11.5a3 3 0 0 0 4 0"/><path d="M7 15.5 5.5 21M17 15.5 18.5 21"/>`,
	"dumbbell": `<path d="M3 9.5v5M6 7.5v9M18 7.5v9M21 9.5v5"/><path d="M6 12h12"/>`,
	"tool":     `<path d="M14.5 3.5a5 5 0 0 0-4.6 7l-6.3 6.3a2 2 0 0 0 2.8 2.8l6.3-6.3a5 5 0 0 0 6.1-6.4l-2.9 2.9-2.5-2.5 2.9-2.9a5 5 0 0 0-1.8-.9z"/>`,
	"card":     `<rect x="2.5" y="5" width="19" height="14" rx="2"/><path d="M2.5 9.5h19"/><path d="M6 15h4"/>`,
	"piggy":    `<path d="M4 12.5c0-3.6 3.4-6 8-6 1 0 2 .1 2.9.4L18 4.5v3.2c1 .9 1.7 2 2 3.3h1.5v4H20c-.5 1-1.3 1.9-2.3 2.5v2.3h-3v-1.4a12 12 0 0 1-4.4 0v1.4h-3v-2.3C5.3 16.4 4 14.6 4 12.5z"/><path d="M8.5 11.5h.01"/>`,
	"wallet":   `<path d="M3 7.5a2 2 0 0 1 2-2h12v3"/><path d="M3 7.5v10a2 2 0 0 0 2 2h14a1 1 0 0 0 1-1v-9a1 1 0 0 0-1-1H5"/><path d="M16.5 13h.01"/>`,
	"coins":    `<ellipse cx="9" cy="6.5" rx="6" ry="2.5"/><path d="M3 6.5v4c0 1.4 2.7 2.5 6 2.5s6-1.1 6-2.5v-4"/><path d="M9 13v4c0 1.4 2.7 2.5 6 2.5s6-1.1 6-2.5v-6"/><ellipse cx="15" cy="11" rx="6" ry="2.5"/>`,
	"chart":    `<path d="M3.5 20.5h17"/><path d="M4 16.5 9.5 11l3.5 3.5 6.5-7"/><path d="M20 4.5h-4.5M20 4.5V9"/>`,
	"tag":      `<path d="M11 3.5H4.5a1 1 0 0 0-1 1V11l9 9a1.5 1.5 0 0 0 2.1 0l5.4-5.4a1.5 1.5 0 0 0 0-2.1z"/><path d="M8 8h.01"/>`,
}

// iconKeywords maps a substring of a category name to an icon. German and
// English are listed together because the catalogs are, and a household may
// well name its categories in either.
var iconKeywords = []struct {
	match string
	icon  string
}{
	// The first match wins, so "haushalt" has to come before "haus".
	{"haushalt", "tool"}, {"einrichtung", "tool"}, {"möbel", "tool"},
	{"miete", "home"}, {"wohn", "home"}, {"rent", "home"}, {"haus", "home"},
	{"strom", "bolt"}, {"energ", "bolt"}, {"electric", "bolt"},
	{"gas", "flame"}, {"heiz", "flame"}, {"heating", "flame"},
	{"wasser", "droplet"}, {"water", "droplet"}, {"nebenkost", "droplet"},
	{"internet", "wifi"}, {"wlan", "wifi"}, {"dsl", "wifi"},
	{"telefon", "phone"}, {"handy", "phone"}, {"mobilfunk", "phone"}, {"phone", "phone"},
	{"auto", "car"}, {"kfz", "car"}, {"benzin", "car"}, {"tank", "car"}, {"car", "car"},
	{"bahn", "bus"}, {"bus", "bus"}, {"ticket", "bus"}, {"nahverkehr", "bus"}, {"transit", "bus"},
	{"lebensmittel", "cart"}, {"einkauf", "cart"}, {"supermarkt", "cart"},
	{"grocer", "cart"}, {"shopping", "cart"},
	{"restaurant", "utensils"}, {"essen", "utensils"}, {"gastro", "utensils"}, {"dining", "utensils"},
	{"versicher", "shield"}, {"insurance", "shield"},
	{"gesundheit", "heart"}, {"arzt", "heart"}, {"health", "heart"},
	{"medikament", "pill"}, {"apotheke", "pill"}, {"pharma", "pill"},
	{"bildung", "school"}, {"schule", "school"}, {"kurs", "school"}, {"education", "school"},
	{"geschenk", "gift"}, {"gift", "gift"}, {"spende", "gift"},
	{"urlaub", "plane"}, {"reise", "plane"}, {"travel", "plane"}, {"holiday", "plane"},
	{"kino", "film"}, {"unterhalt", "film"}, {"entertain", "film"},
	{"abo", "play"}, {"streaming", "play"}, {"subscription", "play"},
	{"freizeit", "ticket"}, {"leisure", "ticket"}, {"hobby", "ticket"},
	{"kleidung", "shirt"}, {"clothes", "shirt"}, {"mode", "shirt"},
	{"haustier", "paw"}, {"tier", "paw"}, {"pet", "paw"},
	{"kind", "baby"}, {"kita", "baby"}, {"child", "baby"}, {"baby", "baby"},
	{"sport", "dumbbell"}, {"fitness", "dumbbell"}, {"gym", "dumbbell"},
	{"reparatur", "tool"}, {"instandhalt", "tool"}, {"repair", "tool"}, {"renovier", "tool"}, {"kredit", "card"}, {"rate", "card"}, {"darlehen", "card"}, {"loan", "card"},
	{"sparen", "piggy"}, {"sparrate", "piggy"}, {"rücklage", "piggy"}, {"saving", "piggy"},
	{"gehalt", "wallet"}, {"lohn", "wallet"}, {"salary", "wallet"}, {"income", "wallet"},
	{"einnahm", "coins"}, {"nebenjob", "coins"}, {"bonus", "coins"},
	{"kapital", "chart"}, {"zins", "chart"}, {"dividende", "chart"}, {"invest", "chart"},
}

// IconKeys lists every available icon in a stable order for the picker.
func IconKeys() []string {
	out := make([]string, 0, len(iconPaths))
	for k := range iconPaths {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// GuessIcon derives an icon from a category name, so a household that never
// opens the picker still gets recognizable symbols.
func GuessIcon(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, k := range iconKeywords {
		if strings.Contains(lower, k.match) {
			return k.icon
		}
	}
	return iconFallback
}

// cleanIcon keeps only keys the icon set actually knows.
func cleanIcon(key string) string {
	if _, ok := iconPaths[strings.TrimSpace(key)]; ok {
		return strings.TrimSpace(key)
	}
	return ""
}

// IconOr returns an icon key, falling back to a guess from the name. A stored
// icon can be empty — the API lets a caller leave it out — so the guess belongs
// here at the point of drawing rather than at the point of saving, which also
// gives categories written before this rule existed a symbol.
func IconOr(icon, name string) string {
	if _, ok := iconPaths[icon]; ok {
		return icon
	}
	return GuessIcon(name)
}

// iconSVG renders an icon. The markup is a package constant, never user input,
// so it can be written unescaped.
func iconSVG(key string) templ.Component {
	body, ok := iconPaths[key]
	if !ok {
		body = iconPaths[iconFallback]
	}
	return templ.Raw(`<svg viewBox="0 0 24 24" ` + iconStroke +
		` aria-hidden="true" focusable="false">` + body + `</svg>`)
}

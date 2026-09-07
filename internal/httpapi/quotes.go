package httpapi

import "math/rand"

// quotes feeds Home's motivational quote card — deliberately not a
// coaching/cadence feature (SPEC.md §13 rejects curriculum guidance),
// just a rotating bit of flavor. Empty until the real list is supplied;
// randomQuote handles that gracefully rather than panicking.
var quotes []string

// randomQuote picks one quote at random, or "" if the list is still
// empty — the quote card simply doesn't render in that case.
func randomQuote() string {
	if len(quotes) == 0 {
		return ""
	}
	return quotes[rand.Intn(len(quotes))]
}

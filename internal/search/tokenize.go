package search

import (
	"strings"
)

// foldText lower-cases, trims, and collapses whitespace. It is the
// human-facing form used for substring matches, so it keeps hyphens.
func foldText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func foldLower(value string) string {
	return strings.ToLower(foldText(value))
}

// queryWords splits free text into lower-cased whitespace words. Each word
// must match independently (AND across words).
func queryWords(text string) []string {
	return strings.Fields(strings.ToLower(strings.TrimSpace(text)))
}

// uniqueNormalized de-duplicates normalized terms and returns them in their
// first-seen order.
func uniqueNormalized(terms []string) []string {
	seen := make(map[string]struct{}, len(terms))
	unique := make([]string, 0, len(terms))
	for _, term := range terms {
		if term == "" {
			continue
		}
		if _, exists := seen[term]; exists {
			continue
		}
		seen[term] = struct{}{}
		unique = append(unique, term)
	}
	return unique
}

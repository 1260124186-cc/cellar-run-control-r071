package search

import (
	"strings"
	"unicode"
)

// Tokens is the analyzed form of a document field or a query. The token set
// combines three complementary views:
//
//   - word tokens: alphanumeric runs between separators ("vsl", "201");
//   - folded tokens: the same runs with non-alphanumeric, non-whitespace
//     characters removed but the runs kept joined ("VSL-201" -> "vsl201"), so
//     a code matches whether or not the operator types its punctuation;
//   - grams: unigrams and overlapping bigrams of CJK runs, so queries such as
//     "高度" match "高度数" without any external text-processing dependency.
type Tokens map[string]struct{}

type termParts struct {
	// folded holds the whitespace-delimited, punctuation-joined form of each
	// latin/digit run, e.g. "VSL-201" -> "vsl201".
	folded []string
	// words holds the individual latin/digit runs, e.g. "vsl", "201".
	words []string
	// grams holds CJK unigrams and overlapping bigrams.
	grams []string
}

func tokenize(value string) Tokens {
	parts := analyze(value)
	tokens := Tokens{}
	for _, value := range parts.folded {
		tokens[value] = struct{}{}
	}
	for _, value := range parts.words {
		tokens[value] = struct{}{}
	}
	for _, value := range parts.grams {
		tokens[value] = struct{}{}
	}
	return tokens
}

// analyze breaks free text into its folded, word, and CJK-gram pieces. Each
// whitespace-delimited term produces one folded token; punctuation breaks
// word tokens but stays transparent to the folded form.
func analyze(value string) termParts {
	parts := termParts{}
	var latin strings.Builder
	var folded strings.Builder

	flushWord := func() {
		if latin.Len() > 0 {
			parts.words = append(parts.words, latin.String())
			latin.Reset()
		}
	}
	flushFolded := func() {
		if folded.Len() > 0 {
			parts.folded = append(parts.folded, folded.String())
			folded.Reset()
		}
	}
	var cjk []rune
	flushCJK := func() {
		if len(cjk) == 0 {
			return
		}
		for _, r := range cjk {
			parts.grams = append(parts.grams, string(r))
		}
		if len(cjk) >= 2 {
			for i := 0; i+2 <= len(cjk); i++ {
				parts.grams = append(parts.grams, string(cjk[i:i+2]))
			}
		}
		cjk = nil
	}

	for _, r := range value {
		switch {
		case isCJK(r):
			flushWord()
			flushFolded()
			cjk = append(cjk, unicode.ToLower(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			flushCJK()
			lowered := unicode.ToLower(r)
			latin.WriteRune(lowered)
			folded.WriteRune(lowered)
		default:
			flushWord()
			flushCJK()
			if unicode.IsSpace(r) {
				flushFolded()
			}
		}
	}
	flushWord()
	flushFolded()
	flushCJK()
	return parts
}

func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0x20000 && r <= 0x2A6DF)
}

// Matches reports whether every whitespace-delimited term in the query is
// present in the document token set. Both query and document pass through the
// same analysis, so a query never observes a representation the index cannot
// contain.
//
// A term matches when its folded form is present as a token or prefix (so
// "vsl-900" and "vsl900" reach the same code) or, when that fails, all of its
// word pieces and CJK grams are present. The all-pieces rule is what keeps a
// shared prefix such as "vsl" in "vsl-900" from matching "VSL-301".
func Matches(document Tokens, query string) bool {
	for _, term := range strings.Fields(strings.TrimSpace(query)) {
		if !matchesTerm(document, analyze(term)) {
			return false
		}
	}
	return true
}

func matchesTerm(document Tokens, parts termParts) bool {
	for _, folded := range parts.folded {
		if tokenPrefixPresent(document, folded) {
			return true
		}
	}
	for _, word := range parts.words {
		if !tokenPrefixPresent(document, word) {
			return false
		}
	}
	for _, gram := range parts.grams {
		if !tokenPrefixPresent(document, gram) {
			return false
		}
	}
	// A term made solely of punctuation yields no pieces and must not match.
	return len(parts.folded)+len(parts.words)+len(parts.grams) > 0
}

func tokenPrefixPresent(document Tokens, prefix string) bool {
	for token := range document {
		if token == prefix || strings.HasPrefix(token, prefix) {
			return true
		}
	}
	return false
}

// Analyze exposes the analyzer for one field so callers re-validating against
// the authoritative snapshot use the exact same analysis as the index.
func Analyze(value string) Tokens {
	return tokenize(value)
}

// AnalyzeAll merges the tokens of several fields.
func AnalyzeAll(fields []string) Tokens {
	merged := Tokens{}
	for _, field := range fields {
		for token := range tokenize(field) {
			merged[token] = struct{}{}
		}
	}
	return merged
}

// MergeTokens returns the union of two token sets.
func MergeTokens(left, right Tokens) Tokens {
	merged := Tokens{}
	for token := range left {
		merged[token] = struct{}{}
	}
	for token := range right {
		merged[token] = struct{}{}
	}
	return merged
}

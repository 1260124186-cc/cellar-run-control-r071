package domain

import (
	"regexp"
	"strings"
)

const maxTagsPerEntity = 8

var tagPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// NormalizeTags lower-cases, trims, de-duplicates, and drops empty tags.
// Order is preserved after de-duplication so persisted tags stay readable.
func NormalizeTags(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		tag := strings.ToLower(strings.TrimSpace(value))
		if tag == "" {
			continue
		}
		if _, exists := seen[tag]; exists {
			continue
		}
		seen[tag] = struct{}{}
		normalized = append(normalized, tag)
	}
	return normalized
}

// ValidateTags enforces the search tag shape accepted on every searchable
// entity. Tags are optional but must be normalized before validation.
func ValidateTags(tags []string) error {
	if len(tags) > maxTagsPerEntity {
		return FieldError(CodeInvalidInput, "tags", "must contain at most 8 entries")
	}
	for _, tag := range tags {
		if len(tag) < 1 || len(tag) > 32 || !tagPattern.MatchString(tag) {
			return FieldError(CodeInvalidInput, "tags",
				"each tag must be 1 to 32 lowercase letters, digits, or internal hyphens")
		}
	}
	return nil
}

func cloneStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return append([]string(nil), values...)
}

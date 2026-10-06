package domain

import (
	"time"
)

const (
	maxTagCount  = 24
	maxTagLength = 40
)

// NormalizeTags canonicalizes free-form tags into stable, deduplicated search
// metadata. Tags are case-folded and whitespace-compressed; they deliberately
// keep hyphens so that, for example, "high-gravity" stays one token.
func NormalizeTags(values []string) []string {
	seen := map[string]struct{}{}
	tags := make([]string, 0, len(values))
	for _, value := range values {
		key := NameKey(value)
		if key == "" {
			continue
		}
		if len([]rune(key)) > maxTagLength {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		tags = append(tags, key)
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}

// ValidateTags enforces the bounds on user-supplied tags before normalization.
func ValidateTags(values []string) error {
	if len(values) > maxTagCount {
		return FieldError(CodeInvalidInput, "tags", "must contain at most 24 entries")
	}
	for _, value := range values {
		trimmed := NormalizeName(value)
		if trimmed == "" {
			return FieldError(CodeInvalidInput, "tags", "entries must not be empty")
		}
		if len([]rune(NameKey(trimmed))) > maxTagLength {
			return FieldError(CodeInvalidInput, "tags",
				"entries must not exceed 40 characters")
		}
	}
	return nil
}

// ReplaceTags attaches search metadata to a formula. Tags are retrieval
// metadata rather than process data, so they can change in any lifecycle state
// without breaking the immutability of an approved formula's process fields.
func (f *Formula) ReplaceTags(tags []string, now time.Time) {
	f.Tags = append([]string(nil), tags...)
	f.UpdatedAt = now.UTC()
	f.Version++
}

// ReplaceTags attaches search metadata to a vessel.
func (v *Vessel) ReplaceTags(tags []string, now time.Time) {
	v.Tags = append([]string(nil), tags...)
	v.UpdatedAt = now.UTC()
	v.Version++
}

// ReplaceTags attaches search metadata to a run, including completed and
// aborted runs so historical records remain discoverable.
func (r *FermentationRun) ReplaceTags(tags []string, now time.Time) {
	r.Tags = append([]string(nil), tags...)
	r.UpdatedAt = now.UTC()
	r.Version++
}

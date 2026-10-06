package search

import (
	"sort"
	"strings"
	"time"
)

// Kind identifies which aggregate a search document represents. The kind is
// stored separately from the identifier so that a coincidentally shared id
// across collections can never merge or cross-contaminate results.
type Kind string

const (
	KindFormula Kind = "formula"
	KindVessel  Kind = "vessel"
	KindRun     Kind = "run"
)

// Ref is an immutable pointer to one aggregate. It is the only identity the
// index ever holds; all other facts are re-read from the authoritative
// snapshot when a result is rendered.
type Ref struct {
	Kind Kind
	ID   string
}

// Document carries the minimum searchable facts for one aggregate. Snapshot
// revision and timestamps are captured at build time so ordering is stable for
// the lifetime of an index generation.
type Document struct {
	Ref Ref
	// VesselCode is the vessel's own code for vessel documents and the joined
	// code of the vessel a run currently occupies for run documents. It is a
	// build-time hint only; the authoritative re-check resolves the live
	// association before a result is returned.
	VesselCode string
	Label      string
	State      string
	Tags       []string
	Text       []string
	UpdatedAt  time.Time
}

type doc struct {
	Document
	text Tokens
	tags map[string]struct{}
}

type index struct {
	revision int64
	docs     map[Ref]doc
	order    []Ref
}

func buildIndex(revision int64, documents []Document) *index {
	built := &index{
		revision: revision,
		docs:     make(map[Ref]doc, len(documents)),
		order:    make([]Ref, 0, len(documents)),
	}
	for _, document := range documents {
		entry := doc{
			Document: document,
			text:     Tokens{},
			tags:     make(map[string]struct{}, len(document.Tags)),
		}
		for _, field := range document.Text {
			for token := range tokenize(field) {
				entry.text[token] = struct{}{}
			}
		}
		for _, tag := range document.Tags {
			for token := range tokenize(tag) {
				entry.text[token] = struct{}{}
			}
			entry.tags[tag] = struct{}{}
		}
		if _, exists := built.docs[document.Ref]; exists {
			continue
		}
		built.docs[document.Ref] = entry
		built.order = append(built.order, document.Ref)
	}
	return built
}

// Filter expresses the structured portion of a query. Empty fields are
// unconstrained. These are coarse filters only; the authoritative re-check at
// render time decides whether a candidate is actually returned.
type Filter struct {
	Kinds      []Kind
	States     []string
	VesselCode string
	Text       string
	Tags       []string
}

func (f Filter) accepts(entry doc) bool {
	if len(f.Kinds) > 0 {
		matched := false
		for _, kind := range f.Kinds {
			if entry.Ref.Kind == kind {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if len(f.States) > 0 {
		matched := false
		for _, state := range f.States {
			if entry.State == state {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	if f.VesselCode != "" && entry.VesselCode != f.VesselCode {
		return false
	}
	if strings.TrimSpace(f.Text) != "" && !Matches(entry.text, f.Text) {
		return false
	}
	if len(f.Tags) > 0 {
		for _, tag := range f.Tags {
			if _, found := entry.tags[tag]; !found {
				return false
			}
		}
	}
	return true
}

// sortedRefs returns all document references in the requested deterministic
// order. Updated-at ordering uses RFC3339 strings so that ties break on the
// immutable (kind, id) pair.
func sortedRefs(index *index, order SortOrder, descending bool) []Ref {
	refs := make([]Ref, 0, len(index.order))
	for _, ref := range index.order {
		refs = append(refs, ref)
	}
	less := func(a, b Ref) bool {
		ea := index.docs[a]
		eb := index.docs[b]
		var before bool
		if order == SortUpdated {
			at := ea.UpdatedAt.UTC().Format(time.RFC3339Nano)
			bt := eb.UpdatedAt.UTC().Format(time.RFC3339Nano)
			before = at < bt
			if at == bt {
				before = refKey(a) < refKey(b)
			}
		} else {
			la := strings.ToLower(ea.Label)
			lb := strings.ToLower(eb.Label)
			before = la < lb
			if la == lb {
				before = refKey(a) < refKey(b)
			}
		}
		if descending {
			return !before
		}
		return before
	}
	sort.Slice(refs, func(i, j int) bool { return less(refs[i], refs[j]) })
	return refs
}

func refKey(ref Ref) string {
	return string(ref.Kind) + "\x00" + ref.ID
}

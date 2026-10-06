package search

import (
	"sort"
	"strings"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

const timeFormat = time.RFC3339

// Search runs a paginated query. The candidate ordering comes from one pinned
// immutable generation (stable sort, resumable cursor), but each candidate is
// resolved against the current live snapshot. An entity that the lagging
// index still lists but the snapshot no longer contains is skipped, and every
// vessel fact is read from the same live snapshot, never copied between hits.
func (e *Engine) Search(query Query) (Page, error) {
	if query.Limit < 1 {
		query.Limit = 25
	}
	current := e.active.Load()
	if current == nil {
		return Page{}, domain.NewError(domain.CodeSearchRebuilding,
			"search index is not available yet")
	}
	pinned := current
	var resumeAfter *cursorKey
	if strings.TrimSpace(query.Cursor) != "" {
		cursor, err := DecodeCursor(query.Cursor)
		if err != nil {
			return Page{}, err
		}
		generation := e.generationBySerial(cursor.Serial)
		if generation == nil {
			return Page{}, domain.NewError(domain.CodeSearchStale,
				"cursor points at an expired index generation; restart without a cursor")
		}
		pinned = generation
		resumeAfter = &cursor.Key
	}

	kindSet := make(map[Kind]struct{}, len(query.Kinds))
	for _, kind := range query.Kinds {
		kindSet[kind] = struct{}{}
	}
	stateSet := make(map[string]struct{}, len(query.States))
	for _, state := range query.States {
		stateSet[state] = struct{}{}
	}
	requiredTags := normalizeTagTerms(query.Tags)
	vesselCode := strings.ToUpper(strings.TrimSpace(query.VesselCode))
	terms := queryWords(query.Text)

	start := 0
	if resumeAfter != nil {
		boundary := -1
		for index, doc := range pinned.documents {
			if keysEqual(doc.key(), *resumeAfter) {
				boundary = index
				break
			}
		}
		if boundary < 0 {
			return Page{}, domain.NewError(domain.CodeSearchCursor,
				"cursor does not match its index generation")
		}
		start = boundary + 1
	}

	var live domain.Snapshot
	if err := e.store.View(func(view domain.Snapshot) error {
		live = view
		return nil
	}); err != nil {
		return Page{}, err
	}

	results := make([]Result, 0, query.Limit)
	for index := start; index < len(pinned.documents); index++ {
		doc := pinned.documents[index]
		if len(kindSet) > 0 {
			if _, ok := kindSet[doc.Kind]; !ok {
				continue
			}
		}
		if len(terms) > 0 && !matchesText(doc, terms) {
			continue
		}
		result, ok := resolveDocument(live, doc, requiredTags, stateSet,
			vesselCode, query.IncludeArchived)
		if !ok {
			continue
		}
		results = append(results, result)
		if len(results) == query.Limit {
			break
		}
	}

	hasMore := false
	var nextCursor string
	if len(results) == query.Limit {
		lastDoc, found := pinned.locate(lastResultKey(results[len(results)-1]))
		if found {
			for index := lastDoc + 1; index < len(pinned.documents); index++ {
				candidate := pinned.documents[index]
				if len(kindSet) > 0 {
					if _, ok := kindSet[candidate.Kind]; !ok {
						continue
					}
				}
				if len(terms) > 0 && !matchesText(candidate, terms) {
					continue
				}
				if _, ok := resolveDocument(live, candidate, requiredTags, stateSet,
					vesselCode, query.IncludeArchived); ok {
					hasMore = true
					break
				}
			}
		}
		last := results[len(results)-1]
		if doc, ok := pinned.locate(lastResultKey(last)); ok {
			nextCursor = encodeCursor(Cursor{Serial: pinned.serial, Key: pinned.documents[doc].key()})
		}
	}

	return Page{
		Results:      results,
		NextCursor:   nextCursor,
		HasMore:      hasMore,
		Generation:   pinned.serial,
		LiveRevision: live.Revision,
	}, nil
}

func lastResultKey(result Result) cursorKey {
	updatedAt, err := time.Parse(timeFormat, result.UpdatedAt)
	if err != nil {
		updatedAt = time.Time{}
	}
	return cursorKey{UpdatedAt: updatedAt, Kind: result.Ref.Kind, ID: result.Ref.ID}
}

func keysEqual(a, b cursorKey) bool {
	return a.UpdatedAt.Equal(b.UpdatedAt) && a.Kind == b.Kind && a.ID == b.ID
}

// matchesText requires every whitespace-separated query word to appear in at
// least one folded field, either verbatim or with hyphens removed on both
// sides ("fv100" matches "FV-100", "restart-test" matches the tag).
func matchesText(doc Document, words []string) bool {
	for _, word := range words {
		if !oneWordMatches(doc, word) {
			return false
		}
	}
	return true
}

func oneWordMatches(doc Document, word string) bool {
	compactWord := strings.ReplaceAll(word, "-", "")
	for _, field := range doc.Fields {
		if strings.Contains(field, word) {
			return true
		}
		if strings.Contains(strings.ReplaceAll(field, "-", ""), compactWord) {
			return true
		}
	}
	return false
}

func normalizeTagTerms(tags []string) []string {
	normalized := make([]string, 0, len(tags))
	for _, tag := range tags {
		if folded := foldLower(tag); folded != "" {
			normalized = append(normalized, folded)
		}
	}
	return uniqueNormalized(normalized)
}

// resolveDocument re-reads the document's entity from the live snapshot and
// applies every non-text filter there. The boolean is false when the entity
// is gone, archived, or does not pass the filters.
func resolveDocument(
	snapshot domain.Snapshot,
	doc Document,
	requiredTags []string,
	stateSet map[string]struct{},
	vesselCode string,
	includeArchived bool,
) (Result, bool) {
	switch doc.Kind {
	case KindFormula:
		formula, ok := snapshot.Formulas[doc.ID]
		if !ok {
			return Result{}, false
		}
		if !includeArchived && formula.State == domain.FormulaRetired {
			return Result{}, false
		}
		if !passState(string(formula.State), stateSet) || !passTags(formula.Tags, requiredTags) {
			return Result{}, false
		}
		return Result{
			Ref:       refFor(KindFormula, formula.ID),
			Label:     formula.Name,
			Detail:    formula.Style,
			State:     string(formula.State),
			Tags:      copiedTags(formula.Tags),
			UpdatedAt: formula.UpdatedAt.UTC().Format(timeFormat),
		}, true
	case KindVessel:
		vessel, ok := snapshot.Vessels[doc.ID]
		if !ok {
			return Result{}, false
		}
		if !includeArchived && vessel.State == domain.VesselRetired {
			return Result{}, false
		}
		if !passState(string(vessel.State), stateSet) || !passTags(vessel.Tags, requiredTags) {
			return Result{}, false
		}
		if vesselCode != "" && vessel.Code != vesselCode {
			return Result{}, false
		}
		return Result{
			Ref:       refFor(KindVessel, vessel.ID),
			Label:     vessel.Code,
			Detail:    capacityLabel(vessel.CapacityL),
			State:     string(vessel.State),
			Tags:      copiedTags(vessel.Tags),
			UpdatedAt: vessel.UpdatedAt.UTC().Format(timeFormat),
		}, true
	case KindRun:
		run, ok := snapshot.Runs[doc.ID]
		if !ok {
			return Result{}, false
		}
		if !includeArchived && run.State == domain.RunAborted {
			return Result{}, false
		}
		if !passState(string(run.State), stateSet) || !passTags(run.Tags, requiredTags) {
			return Result{}, false
		}
		result := Result{
			Ref:       refFor(KindRun, run.ID),
			Label:     run.Code,
			Detail:    runLabel(snapshot, run),
			State:     string(run.State),
			Tags:      copiedTags(run.Tags),
			UpdatedAt: run.UpdatedAt.UTC().Format(timeFormat),
			FormulaID: run.FormulaID,
		}
		if run.VesselID != nil {
			// The vessel is read from the same live snapshot, so a run can
			// never expose facts belonging to a different vessel.
			if vessel, ok := snapshot.Vessels[*run.VesselID]; ok {
				result.VesselID = vessel.ID
				result.VesselCode = vessel.Code
			}
		}
		if vesselCode != "" && result.VesselCode != vesselCode {
			return Result{}, false
		}
		return result, true
	}
	return Result{}, false
}

func passState(state string, allowed map[string]struct{}) bool {
	if len(allowed) == 0 {
		return true
	}
	_, ok := allowed[state]
	return ok
}

func passTags(actual, required []string) bool {
	if len(required) == 0 {
		return true
	}
	present := make(map[string]struct{}, len(actual))
	for _, tag := range actual {
		present[tag] = struct{}{}
	}
	for _, tag := range required {
		if _, ok := present[tag]; !ok {
			return false
		}
	}
	return true
}

func copiedTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}
	return append([]string(nil), tags...)
}

func refFor(kind Kind, id string) Ref {
	return Ref{Kind: kind, ID: id, Path: detailPath(kind, id)}
}

func detailPath(kind Kind, id string) string {
	return "/v1/" + plural(kind) + "/" + id
}

func plural(kind Kind) string {
	switch kind {
	case KindFormula:
		return "formulas"
	case KindVessel:
		return "vessels"
	default:
		return "runs"
	}
}

func capacityLabel(liters float64) string {
	return formatFloat(liters) + " L"
}

func runLabel(snapshot domain.Snapshot, run domain.FermentationRun) string {
	parts := make([]string, 0, 2)
	if formula, ok := snapshot.Formulas[run.FormulaID]; ok {
		parts = append(parts, formula.Name)
	}
	if run.VesselID != nil {
		if vessel, ok := snapshot.Vessels[*run.VesselID]; ok {
			parts = append(parts, vessel.Code)
		}
	}
	return strings.Join(parts, " · ")
}

// SortedTagSuggestions returns the distinct tags currently applied in the
// live snapshot, for client filter UIs.
func SortedTagSuggestions(snapshot domain.Snapshot) []string {
	seen := map[string]struct{}{}
	for _, formula := range snapshot.Formulas {
		for _, tag := range formula.Tags {
			seen[tag] = struct{}{}
		}
	}
	for _, vessel := range snapshot.Vessels {
		for _, tag := range vessel.Tags {
			seen[tag] = struct{}{}
		}
	}
	for _, run := range snapshot.Runs {
		for _, tag := range run.Tags {
			seen[tag] = struct{}{}
		}
	}
	tags := make([]string, 0, len(seen))
	for tag := range seen {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags
}

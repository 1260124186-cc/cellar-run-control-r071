package search

import (
	"sort"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

// Kind identifies which collection a search document belongs to.
type Kind string

const (
	KindFormula Kind = "formula"
	KindVessel  Kind = "vessel"
	KindRun     Kind = "run"
)

// kindRank breaks updated_at ties with a fixed cross-entity ordering.
func kindRank(kind Kind) int {
	switch kind {
	case KindFormula:
		return 0
	case KindVessel:
		return 1
	case KindRun:
		return 2
	default:
		return 3
	}
}

// Document is the immutable indexed projection of one entity. The only entity
// facts it carries are folded search fields; every other fact returned to a
// client is re-read from the live snapshot at query time.
type Document struct {
	Kind      Kind
	ID        string
	UpdatedAt time.Time
	Fields    []string // folded, lower-cased human text for substring matches
}

func (d Document) key() cursorKey {
	return cursorKey{UpdatedAt: d.UpdatedAt.UTC(), Kind: d.Kind, ID: d.ID}
}

func formulaDocument(formula domain.Formula) Document {
	return Document{
		Kind:      KindFormula,
		ID:        formula.ID,
		UpdatedAt: formula.UpdatedAt,
		Fields:    uniqueNormalized(append([]string{foldLower(formula.Name), foldLower(formula.Style)}, formula.Tags...)),
	}
}

func vesselDocument(vessel domain.Vessel) Document {
	return Document{
		Kind:      KindVessel,
		ID:        vessel.ID,
		UpdatedAt: vessel.UpdatedAt,
		Fields:    uniqueNormalized(append([]string{foldLower(vessel.Code)}, vessel.Tags...)),
	}
}

func runDocument(run domain.FermentationRun) Document {
	return Document{
		Kind:      KindRun,
		ID:        run.ID,
		UpdatedAt: run.UpdatedAt,
		Fields:    uniqueNormalized(append([]string{foldLower(run.Code)}, run.Tags...)),
	}
}

// buildDocuments derives a complete, self-consistent document set from one
// snapshot. A rebuild and a live refresh run the exact same code path, so an
// interrupted build can never publish a partial index: the result is swapped
// in as one immutable generation only after this function returns.
func buildDocuments(snapshot domain.Snapshot) []Document {
	documents := make([]Document, 0, len(snapshot.Formulas)+len(snapshot.Vessels)+len(snapshot.Runs))
	for _, formula := range snapshot.Formulas {
		documents = append(documents, formulaDocument(formula.Clone()))
	}
	for _, vessel := range snapshot.Vessels {
		documents = append(documents, vesselDocument(vessel.Clone()))
	}
	for _, run := range snapshot.Runs {
		documents = append(documents, runDocument(run.Clone()))
	}
	sort.Slice(documents, func(i, j int) bool {
		left, right := documents[i].key(), documents[j].key()
		if !left.UpdatedAt.Equal(right.UpdatedAt) {
			return left.UpdatedAt.After(right.UpdatedAt)
		}
		if left.Kind != right.Kind {
			return kindRank(left.Kind) < kindRank(right.Kind)
		}
		return left.ID < right.ID
	})
	return documents
}

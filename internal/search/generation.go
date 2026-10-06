package search

import "time"

// generation is one immutable, completely built index revision. Searches pin
// their sort order and cursor to a generation, while hit correctness is always
// validated against the current live snapshot.
type generation struct {
	serial    int64
	sourceRev int64
	builtAt   time.Time
	documents []Document
}

func newGeneration(serial, sourceRev int64, builtAt time.Time, documents []Document) *generation {
	return &generation{
		serial:    serial,
		sourceRev: sourceRev,
		builtAt:   builtAt.UTC(),
		documents: documents,
	}
}

// locate returns the ordered index of the document identified by key. Only
// kind and ID are consulted because they uniquely identify a document within
// a generation and survive cursor round-trips exactly.
func (g *generation) locate(key cursorKey) (int, bool) {
	for index, doc := range g.documents {
		if doc.ID == key.ID && doc.Kind == key.Kind {
			return index, true
		}
	}
	return 0, false
}

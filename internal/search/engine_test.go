package search

import (
	"sync"
	"testing"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

type fakeReader struct {
	mu       sync.RWMutex
	snapshot domain.Snapshot
}

func (f *fakeReader) View(visitor func(domain.Snapshot) error) error {
	f.mu.RLock()
	snapshot := f.snapshot.Clone()
	f.mu.RUnlock()
	return visitor(snapshot)
}

func (f *fakeReader) set(snapshot domain.Snapshot) {
	f.mu.Lock()
	f.snapshot = snapshot
	f.mu.Unlock()
}

func vesselSnapshot(revision int64, codesAndStates ...string) domain.Snapshot {
	snapshot := domain.EmptySnapshot()
	snapshot.Revision = revision
	for i := 0; i+1 < len(codesAndStates); i += 2 {
		id := "vessel_" + codesAndStates[i]
		snapshot.Vessels[id] = domain.Vessel{
			ID:    id,
			Code:  codesAndStates[i],
			State: domain.VesselState(codesAndStates[i+1]),
		}
	}
	return snapshot
}

func waitForRevision(engine *Engine, revision int64) bool {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if engine.Revision() == revision {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestEngineRebuildsAndPublishesAtomically(t *testing.T) {
	reader := &fakeReader{snapshot: vesselSnapshot(1, "VSL-1", "available")}
	engine := NewEngine(reader)
	defer engine.Close()
	if !waitForRevision(engine, 1) {
		t.Fatal("first generation was not published")
	}

	page, err := engine.Search(Query{Limit: 10}, func(Ref) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Hits) != 1 {
		t.Fatalf("expected one vessel, got %d", len(page.Hits))
	}

	// A write burst after one vessel is retired: coalescing must eventually
	// publish exactly the newest revision with the vessel still hidden by the
	// caller's admit predicate.
	reader.set(vesselSnapshot(4, "VSL-1", "retired", "VSL-2", "available"))
	for i := 0; i < 50; i++ {
		engine.Invalidate()
	}
	if !waitForRevision(engine, 4) {
		t.Fatal("engine did not converge on the newest snapshot revision")
	}

	admit := func(ref Ref) bool {
		return ref != (Ref{Kind: KindVessel, ID: "vessel_VSL-1"})
	}
	page, err = engine.Search(Query{
		Filter: Filter{Kinds: []Kind{KindVessel}},
		Limit:  10,
	}, admit)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Hits) != 1 || page.Hits[0].Ref.ID != "vessel_VSL-2" {
		t.Fatalf("rebuilt index/admit returned the wrong set: %+v", page.Hits)
	}
}

func TestEngineSearchBeforeReady(t *testing.T) {
	reader := &fakeReader{snapshot: domain.EmptySnapshot()}
	engine := &Engine{reader: reader} // no scheduled build yet
	defer engine.Close()
	if _, err := engine.Search(Query{}, nil); err != ErrIndexNotReady {
		t.Fatalf("expected ErrIndexNotReady, got %v", err)
	}
}

func TestPaginationKeysetCoversAllOnce(t *testing.T) {
	generation := buildIndex(1, []Document{
		{Ref: Ref{Kind: KindFormula, ID: "a"}, Label: "Alpha", Text: []string{"Alpha"}},
		{Ref: Ref{Kind: KindFormula, ID: "b"}, Label: "Bravo", Text: []string{"Bravo"}},
		{Ref: Ref{Kind: KindFormula, ID: "c"}, Label: "Charlie", Text: []string{"Charlie"}},
	})
	engine := &Engine{current: generation}

	seen := map[string]bool{}
	cursor := ""
	for pages := 0; pages < 5; pages++ {
		page, err := engine.Search(Query{
			Filter: Filter{Kinds: []Kind{KindFormula}},
			Limit:  2,
			Cursor: cursor,
		}, func(Ref) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range page.Hits {
			if seen[hit.Ref.ID] {
				t.Fatalf("pagination repeated %s", hit.Ref.ID)
			}
			seen[hit.Ref.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(seen) != 3 {
		t.Fatalf("expected all three documents once, got %v", seen)
	}
}

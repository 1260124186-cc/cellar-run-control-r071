package search

import "testing"

func TestTokenizeCodes(t *testing.T) {
	tokens := tokenize("VSL-201")
	for _, wanted := range []string{"vsl201", "vsl", "201"} {
		if _, ok := tokens[wanted]; !ok {
			t.Fatalf("tokens for %q missing %q: %v", "VSL-201", wanted, tokens)
		}
	}
}

func TestMatchesCodeDoesNotCrossMatch(t *testing.T) {
	document := tokenize("VSL-301 Vessel")
	if !Matches(document, "vsl-301") {
		t.Fatal("folded code should match with punctuation")
	}
	if !Matches(document, "vsl301") {
		t.Fatal("folded code should match without punctuation")
	}
	if Matches(document, "vsl-900") {
		t.Fatal("a shared piece must not match a different full code")
	}
	if Matches(document, "vsl 900") {
		t.Fatal("a multi-piece term must not match with only one piece present")
	}
	if !Matches(document, "vsl") {
		t.Fatal("a bare shared prefix may match")
	}
}

func TestMatchesChineseGrams(t *testing.T) {
	document := tokenize("上周那桶高度数配方")
	for _, query := range []string{"高度", "高度数", "配方"} {
		if !Matches(document, query) {
			t.Fatalf("expected %q to match the CJK document", query)
		}
	}
	unrelated := tokenize("清爽小麦")
	if Matches(unrelated, "高度") {
		t.Fatal("an unrelated CJK query must not match")
	}
}

func TestCursorRejectsDifferentShape(t *testing.T) {
	position := Position{Label: "VSL-1", ID: "vessel_x", Kind: KindVessel}
	query := Query{Sort: SortLabel, Limit: 2}
	cursor, err := EncodeCursor(position, query)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeCursor(cursor, Query{Sort: SortUpdated}); err != ErrInvalidCursor {
		t.Fatal("cursor for label order must not decode under updated order")
	}
	descendingQuery := Query{Sort: SortLabel, Descending: true}
	if _, err := DecodeCursor(cursor, descendingQuery); err != ErrInvalidCursor {
		t.Fatal("cursor must be bound to its direction")
	}
	decoded, err := DecodeCursor(cursor, query)
	if err != nil {
		t.Fatalf("the same shape should decode: %v", err)
	}
	if decoded.ID != position.ID || decoded.Kind != position.Kind {
		t.Fatal("decoded cursor lost its position")
	}
}

// TestStaleIndexIsReconciledByAdmit models a generation built before a vessel
// was retired and before a run changed vessels. The stale index still recalls
// the outdated candidates, but the authoritative admit predicate evaluated on
// the current snapshot must suppress the phantom and the cross-vessel fact.
func TestStaleIndexIsReconciledByAdmit(t *testing.T) {
	// Generation reflects an old world: vessel V is in_use and run R is on
	// vessel A.
	generation := buildIndex(1, []Document{
		{
			Ref:        Ref{Kind: KindVessel, ID: "v"},
			Label:      "VSL-1",
			VesselCode: "VSL-1",
			State:      "in_use",
			Text:       []string{"VSL-1"},
		},
		{
			Ref:        Ref{Kind: KindRun, ID: "r"},
			Label:      "RUN-1",
			VesselCode: "VSL-A",
			State:      "fermenting",
			Text:       []string{"RUN-1", "VSL-A"},
		},
	})
	engine := &Engine{current: generation}

	page, err := engine.Search(Query{
		Filter: Filter{States: []string{"in_use"}},
		Limit:  10,
	}, func(Ref) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Hits) != 0 {
		t.Fatal("archived candidate from a stale generation must be suppressed")
	}

	// The run is recalled under VSL-A by the stale generation but current truth
	// attributes it to VSL-B, so filtering by VSL-A must admit nothing.
	crossVessel, err := engine.Search(Query{
		Filter: Filter{Kinds: []Kind{KindRun}, VesselCode: "VSL-A"},
		Limit:  10,
	}, func(ref Ref) bool {
		if ref == (Ref{Kind: KindRun, ID: "r"}) {
			return false // live re-check resolves VSL-B, not VSL-A
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(crossVessel.Hits) != 0 {
		t.Fatal("a run must not surface under another vessel's code")
	}
}

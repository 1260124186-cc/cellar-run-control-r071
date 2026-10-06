package search

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

// SnapshotReader provides point-in-time clones of the authoritative state. The
// storage store satisfies it directly.
type SnapshotReader interface {
	View(visitor func(domain.Snapshot) error) error
}

// ErrIndexNotReady means no complete generation has been published yet, for
// example immediately after startup while the first rebuild is in flight.
var ErrIndexNotReady = errors.New("search index is still being built")

// Admit is the authoritative re-check applied to every candidate the coarse
// index recalls. It runs against the snapshot the caller is rendering from and
// must confirm existence and every query predicate. This is what keeps stale
// index generations from returning archived, deleted, or cross-vessel facts.
type Admit func(ref Ref) bool

// Hit pairs an admitted reference with the stable position of the document
// that produced it.
type Hit struct {
	Ref      Ref
	Position Position
}

// Page is one slice of an ordered, admitted result set.
type Page struct {
	Hits       []Hit
	NextCursor string
}

// Engine owns the current immutable index generation. Rebuilds run on a
// dedicated goroutine and are fully constructed before an atomic pointer swap,
// so an interrupted rebuild (process restart, shutdown, or a burst of newer
// writes) can never publish a half-populated index. Concurrent rebuild
// requests coalesce: under a write burst the engine rebuilds at most once more
// after the current build finishes.
type Engine struct {
	reader SnapshotReader

	mu      sync.RWMutex
	current *index

	buildMu  sync.Mutex
	pending  bool
	building bool
	stopCh   chan struct{}
	stopped  bool
}

// NewEngine creates an engine and schedules the first rebuild from the current
// snapshot, so the index is reconstructible from scratch on every start and is
// never persisted out of band.
func NewEngine(reader SnapshotReader) *Engine {
	engine := &Engine{
		reader: reader,
		stopCh: make(chan struct{}),
	}
	engine.schedule()
	return engine
}

// Close stops the background rebuild loop. A build already in progress is
// abandoned by leaving its unreferenced index to be garbage collected; the
// previously published generation stays valid until then.
func (e *Engine) Close() {
	e.buildMu.Lock()
	if e.stopped {
		e.buildMu.Unlock()
		return
	}
	e.stopped = true
	e.buildMu.Unlock()
	if e.stopCh != nil {
		close(e.stopCh)
	}
}

// Invalidate marks the current generation stale and schedules a rebuild. It is
// called after each committed write and never blocks the write path.
func (e *Engine) Invalidate() {
	e.schedule()
}

func (e *Engine) schedule() {
	e.buildMu.Lock()
	if e.stopped {
		e.buildMu.Unlock()
		return
	}
	if e.building {
		e.pending = true
		e.buildMu.Unlock()
		return
	}
	e.building = true
	e.buildMu.Unlock()
	go e.rebuildLoop()
}

func (e *Engine) rebuildLoop() {
	for {
		e.rebuildOnce()
		e.buildMu.Lock()
		if e.stopped {
			e.building = false
			e.buildMu.Unlock()
			return
		}
		if e.pending {
			e.pending = false
			e.buildMu.Unlock()
			continue
		}
		e.building = false
		e.buildMu.Unlock()
		return
	}
}

func (e *Engine) rebuildOnce() {
	var documents []Document
	var revision int64
	if err := e.reader.View(func(snapshot domain.Snapshot) error {
		revision = snapshot.Revision
		documents = DocumentsFromSnapshot(snapshot)
		return nil
	}); err != nil {
		return
	}
	generation := buildIndex(revision, documents)
	e.mu.Lock()
	e.current = generation
	e.mu.Unlock()
}

// Ready reports whether at least one complete generation has been published.
func (e *Engine) Ready() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.current != nil
}

// Revision returns the snapshot revision the current generation was built
// from, or zero when no generation is published yet.
func (e *Engine) Revision() int64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.current == nil {
		return 0
	}
	return e.current.revision
}

// Search walks the stable ordering of the current generation, skips to the
// cursor position, and returns up to Limit candidates that the coarse filter
// and the authoritative admit callback both accept.
func (e *Engine) Search(query Query, admit Admit) (Page, error) {
	e.mu.RLock()
	generation := e.current
	e.mu.RUnlock()
	if generation == nil {
		return Page{}, ErrIndexNotReady
	}
	limit := query.Limit
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}

	var position *Position
	if query.Cursor != "" {
		decoded, err := DecodeCursor(query.Cursor, query)
		if err != nil {
			return Page{}, err
		}
		position = &decoded
	}

	ordered := sortedRefs(generation, query.Sort, query.Descending)
	page := Page{Hits: make([]Hit, 0, limit)}
	for _, ref := range ordered {
		entry := generation.docs[ref]
		if position != nil && !afterPosition(query.Sort, query.Descending, entry, *position) {
			continue
		}
		if !query.Filter.accepts(entry) {
			continue
		}
		if admit != nil && !admit(ref) {
			continue
		}
		page.Hits = append(page.Hits, Hit{
			Ref: ref,
			Position: Position{
				Label:     entry.Label,
				UpdatedAt: entry.UpdatedAt,
				ID:        entry.Ref.ID,
				Kind:      entry.Ref.Kind,
			},
		})
		if len(page.Hits) == limit {
			cursor, err := EncodeCursor(page.Hits[len(page.Hits)-1].Position, query)
			if err != nil {
				return Page{}, err
			}
			page.NextCursor = cursor
			break
		}
	}
	return page, nil
}

// rank is the stable sort tuple (sort key, kind, id) used by both ordering and
// cursor positioning.
func (e doc) rank(order SortOrder) (string, string) {
	if order == SortUpdated {
		return e.UpdatedAt.UTC().Format(time.RFC3339Nano), refKey(e.Ref)
	}
	return strings.ToLower(e.Label), refKey(e.Ref)
}

func (p Position) rank(order SortOrder) (string, string) {
	if order == SortUpdated {
		return p.UpdatedAt.UTC().Format(time.RFC3339Nano),
			string(p.Kind) + "\x00" + p.ID
	}
	return strings.ToLower(p.Label), string(p.Kind) + "\x00" + p.ID
}

// afterPosition reports whether entry lies strictly beyond the cursor in the
// traversal direction. Because the rank always ends with (kind, id), two
// different documents can never compare equal.
func afterPosition(order SortOrder, descending bool, entry doc, position Position) bool {
	leftKey, leftRef := entry.rank(order)
	rightKey, rightRef := position.rank(order)
	after := leftKey > rightKey || (leftKey == rightKey && leftRef > rightRef)
	if descending {
		// In descending traversal "beyond the cursor" means a smaller rank;
		// the cursor document itself is skipped either way.
		return !after && !(leftKey == rightKey && leftRef == rightRef)
	}
	return after
}

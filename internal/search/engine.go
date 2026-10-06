package search

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/storage"
)

const (
	// retainedGenerations bounds how many superseded index generations are
	// kept so an in-flight client can finish paging after writes land.
	retainedGenerations = 8
	// nudgeBuffer bounds dropped notifications; refreshes are coalesced and
	// each refresh reads the latest revision, so a dropped nudge is harmless.
	nudgeBuffer = 64
	// nudgeDebounce coalesces a burst of writes into one rebuild.
	nudgeDebounce = 50 * time.Millisecond
)

// Engine maintains rebuildable search generations over the snapshot store.
type Engine struct {
	store   *storage.Store
	now     func() time.Time
	active  atomic.Pointer[generation]
	history atomic.Pointer[[]*generation] // oldest first, newest at the end

	rebuildMu sync.Mutex
	nudge     chan struct{}
	stop      chan struct{}
	done      chan struct{}
	stopped   atomic.Bool
}

// NewEngine registers for commit notifications, builds the first complete
// generation synchronously (the service is never exposed to a half-built
// index), and starts the coalescing refresher.
func NewEngine(store *storage.Store, now func() time.Time) (*Engine, error) {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	engine := &Engine{
		store: store,
		now:   now,
		nudge: make(chan struct{}, nudgeBuffer),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	store.OnCommit(func(int64) { engine.signal() })
	if err := engine.refreshLocked(); err != nil {
		return nil, err
	}
	go engine.loop()
	return engine, nil
}

// Stop ends the background refresher.
func (e *Engine) Stop() {
	if e.stopped.CompareAndSwap(false, true) {
		close(e.stop)
		<-e.done
	}
}

func (e *Engine) signal() {
	if e.stopped.Load() {
		return
	}
	select {
	case e.nudge <- struct{}{}:
	default: // refresh already pending; a dropped nudge is self-healing.
	}
}

func (e *Engine) loop() {
	defer close(e.done)
	for {
		select {
		case <-e.stop:
			return
		case <-e.nudge:
		}
		timer := time.NewTimer(nudgeDebounce)
		select {
		case <-e.stop:
			timer.Stop()
			return
		case <-e.nudge: // drain coalesced notifications
		case <-timer.C:
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		if err := e.Refresh(); err != nil {
			// Re-enqueue so a transient snapshot read failure cannot strand
			// the index at an old generation.
			e.signal()
		}
	}
}

// publish builds a fresh complete generation from the latest snapshot and
// atomically swaps it in. The previous generations remain available for
// cursors already in clients' hands.
func (e *Engine) publish(force bool) error {
	e.rebuildMu.Lock()
	defer e.rebuildMu.Unlock()
	return e.refreshLocked(force)
}

func (e *Engine) refreshLocked(force ...bool) error {
	var snapshot domain.Snapshot
	if err := e.store.View(func(view domain.Snapshot) error {
		snapshot = view
		return nil
	}); err != nil {
		return err
	}
	current := e.active.Load()
	if current != nil && !firstBool(force) && current.sourceRev == snapshot.Revision {
		return nil
	}
	documents := buildDocuments(snapshot)
	serial := int64(1)
	if current != nil {
		serial = current.serial + 1
	}
	next := newGeneration(serial, snapshot.Revision, e.now(), documents)

	var updatedHistory []*generation
	if current != nil {
		previous := *e.history.Load()
		updatedHistory = append(previous, current)
		if len(updatedHistory) > retainedGenerations {
			updatedHistory = updatedHistory[len(updatedHistory)-retainedGenerations:]
		}
	}
	e.history.Store(&updatedHistory)
	e.active.Store(next)
	return nil
}

// Refresh catches the engine up to the latest committed snapshot. It is a
// no-op when the index is already current.
func (e *Engine) Refresh() error { return e.publish(false) }

// Rebuild forces a brand-new generation even when the revision is unchanged.
// It runs to completion before returning, so a restart mid-build simply means
// no swap happened and the next startup builds from scratch.
func (e *Engine) Rebuild() error { return e.publish(true) }

func firstBool(values []bool) bool {
	return len(values) > 0 && values[0]
}

func (e *Engine) generationBySerial(serial int64) *generation {
	if current := e.active.Load(); current != nil && current.serial == serial {
		return current
	}
	for _, candidate := range *e.history.Load() {
		if candidate.serial == serial {
			return candidate
		}
	}
	return nil
}

// Status describes the currently served generation for diagnostics.
type Status struct {
	Generation    int64     `json:"generation"`
	SourceRev     int64     `json:"source_revision"`
	DocumentCount int       `json:"document_count"`
	BuiltAt       time.Time `json:"built_at"`
	LiveRevision  int64     `json:"live_revision"`
	CaughtUp      bool      `json:"caught_up"`
}

// SnapshotStatus reports index freshness without mutating anything.
func (e *Engine) SnapshotStatus() Status {
	current := e.active.Load()
	status := Status{LiveRevision: e.store.Revision()}
	if current != nil {
		status.Generation = current.serial
		status.SourceRev = current.sourceRev
		status.DocumentCount = len(current.documents)
		status.BuiltAt = current.builtAt
		status.CaughtUp = current.sourceRev == status.LiveRevision
	}
	return status
}

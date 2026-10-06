package service

import (
	"strings"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/clock"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/search"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/storage"
)

type Service struct {
	store  *storage.Store
	clock  clock.Clock
	engine *search.Engine
}

func New(store *storage.Store, timeSource clock.Clock) *Service {
	if timeSource == nil {
		timeSource = clock.System{}
	}
	engine := search.NewEngine(store)
	return &Service{store: store, clock: timeSource, engine: engine}
}

// Close releases the background search rebuild loop.
func (s *Service) Close() {
	s.engine.Close()
}

func (s *Service) Revision() int64 {
	return s.store.Revision()
}

// mutate runs a committed snapshot mutation and then tells the search engine
// its current generation is stale. Invalidation is fire-and-forget: a busy
// write burst coalesces rebuilds rather than blocking writers.
func (s *Service) mutate(mutator func(*domain.Snapshot) error) error {
	if err := s.store.Execute(mutator); err != nil {
		return err
	}
	s.engine.Invalidate()
	return nil
}

func (s *Service) checkExpected(current int64, expected *int64) error {
	if expected != nil && current != *expected {
		return domain.NewError(domain.CodeRevisionConflict,
			"the aggregate version does not match X-Expected-Version")
	}
	return nil
}

func generateEntityID(prefix string) (string, error) {
	return domain.NewID(prefix)
}

func generatedRunCode() (string, error) {
	value, err := domain.NewID("run")
	if err != nil {
		return "", err
	}
	suffix := strings.ToUpper(strings.TrimPrefix(value, "run_"))
	return "RUN-" + suffix, nil
}

package service

import (
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/search"
)

// AttachSearchIndex wires the rebuildable search engine into the service. The
// engine builds its first complete generation synchronously before returning.
func (s *Service) AttachSearchIndex(engine *search.Engine) {
	s.searchIndex = engine
}

// SearchPage runs a query against the pinned index generation with live
// snapshot resolution of every hit.
func (s *Service) SearchPage(query search.Query) (search.Page, error) {
	if s.searchIndex == nil {
		return search.Page{}, domain.NewError(domain.CodeSearchRebuilding,
			"search index is not configured")
	}
	return s.searchIndex.Search(query)
}

// RebuildSearchIndex forces a complete generation rebuild and returns only
// after it has been published.
func (s *Service) RebuildSearchIndex() (search.Status, error) {
	if s.searchIndex == nil {
		return search.Status{}, domain.NewError(domain.CodeSearchRebuilding,
			"search index is not configured")
	}
	if err := s.searchIndex.Rebuild(); err != nil {
		return search.Status{}, err
	}
	return s.searchIndex.SnapshotStatus(), nil
}

// SearchIndexStatus reports the served generation and its freshness.
func (s *Service) SearchIndexStatus() (search.Status, error) {
	if s.searchIndex == nil {
		return search.Status{}, domain.NewError(domain.CodeSearchRebuilding,
			"search index is not configured")
	}
	return s.searchIndex.SnapshotStatus(), nil
}

// SearchTags lists the distinct tags present in the live snapshot.
func (s *Service) SearchTags() ([]string, error) {
	var tags []string
	err := s.store.View(func(snapshot domain.Snapshot) error {
		tags = search.SortedTagSuggestions(snapshot)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tags, nil
}

package service

import (
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/search"
)

// SearchInput is the use-case level search request. Structured filters are
// exact; Text is tokenized and prefix-matched.
type SearchInput struct {
	Kinds      []search.Kind
	States     []string
	VesselCode string
	Text       string
	Tags       []string
	Sort       search.SortOrder
	Desc       bool
	Limit      int
	Cursor     string
	// IncludeArchived also returns terminal records (retired formulas and
	// vessels, completed and aborted runs). By default they are hidden.
	IncludeArchived bool
}

// SearchResult renders one admitted aggregate with jump-back details.
type SearchResult struct {
	Kind       search.Kind `json:"kind"`
	ID         string      `json:"id"`
	Label      string      `json:"label"`
	State      string      `json:"state"`
	Tags       []string    `json:"tags"`
	VesselCode string      `json:"vessel_code,omitempty"`
	DetailURL  string      `json:"detail_url"`
	UpdatedAt  time.Time   `json:"updated_at"`
}

// SearchPage is a stable, cursor-driven page of results.
type SearchPage struct {
	Results    []SearchResult `json:"results"`
	NextCursor string         `json:"next_cursor,omitempty"`
	// Revision is the authoritative snapshot revision the page was rendered
	// from, so a client can tell whether an index lagged behind the writes it
	// just issued.
	Revision int64 `json:"revision"`
}

func (s *Service) Search(input SearchInput) (SearchPage, error) {
	filter := search.Filter{
		Kinds:      input.Kinds,
		States:     input.States,
		VesselCode: input.VesselCode,
		Text:       input.Text,
		Tags:       domain.NormalizeTags(input.Tags),
	}
	query := search.Query{
		Filter:     filter,
		Sort:       input.Sort,
		Descending: input.Desc,
		Limit:      input.Limit,
		Cursor:     input.Cursor,
	}

	page := SearchPage{Results: []SearchResult{}}

	// The admit callback runs entirely within one point-in-time snapshot view,
	// the same view used to render results. Every recalled reference is
	// therefore re-validated against current truth: a stale index generation
	// can never surface an object that no longer matches, and a run can never
	// be reported under another vessel's facts.
	err := s.store.View(func(snapshot domain.Snapshot) error {
		page.Revision = snapshot.Revision
		admit := func(ref search.Ref) bool {
			return admits(ref, snapshot, filter, input.IncludeArchived)
		}
		hits, err := s.engine.Search(query, admit)
		if err != nil {
			return mapSearchError(err)
		}
		page.NextCursor = hits.NextCursor
		for _, hit := range hits.Hits {
			result, ok := renderResult(hit.Ref, snapshot)
			if !ok {
				// Defensive: admits already confirmed existence, so this path
				// indicates a corrupted view and must not leak a phantom.
				continue
			}
			page.Results = append(page.Results, result)
		}
		return nil
	})
	if err != nil {
		return SearchPage{}, err
	}
	return page, nil
}

func mapSearchError(err error) error {
	switch err {
	case search.ErrIndexNotReady:
		return domain.NewError(domain.CodeIndexUnavailable,
			"search index is still being built; retry shortly")
	case search.ErrInvalidCursor:
		return domain.FieldError(domain.CodeInvalidInput, "cursor",
			"is invalid or does not match this query")
	default:
		return err
	}
}

// admits is the authoritative predicate: existence first, then every coarse
// filter is re-evaluated against the live aggregate. The run-to-vessel join
// used by the coarse vessel filter is resolved here from current truth, so a
// run whose vessel changed can never surface under another vessel's code.
func admits(
	ref search.Ref,
	snapshot domain.Snapshot,
	filter search.Filter,
	includeArchived bool,
) bool {
	var (
		state      string
		tags       []string
		text       []string
		vesselCode string
		archived   bool
	)
	switch ref.Kind {
	case search.KindFormula:
		formula, ok := snapshot.Formulas[ref.ID]
		if !ok {
			return false
		}
		state, tags, text = string(formula.State), formula.Tags,
			[]string{formula.Name, formula.Style}
		archived = formula.State == domain.FormulaRetired
	case search.KindVessel:
		vessel, ok := snapshot.Vessels[ref.ID]
		if !ok {
			return false
		}
		state, tags, text, vesselCode =
			string(vessel.State), vessel.Tags,
			[]string{vessel.Code}, vessel.Code
		archived = vessel.State == domain.VesselRetired
	case search.KindRun:
		run, ok := snapshot.Runs[ref.ID]
		if !ok {
			return false
		}
		state, tags = string(run.State), run.Tags
		text = []string{run.Code, run.Notes}
		archived = run.State == domain.RunCompleted || run.State == domain.RunAborted
		if run.VesselID != nil {
			if vessel, ok := snapshot.Vessels[*run.VesselID]; ok {
				vesselCode = vessel.Code
				text = append(text, vessel.Code)
			} else if filter.VesselCode != "" {
				// The run names a vessel that is absent from current truth;
				// never attribute it to the vessel being filtered for.
				return false
			}
		}
	default:
		return false
	}

	if !includeArchived && archived {
		return false
	}
	if len(filter.States) > 0 && !containsString(filter.States, state) {
		return false
	}
	if filter.VesselCode != "" && vesselCode != filter.VesselCode {
		return false
	}
	if filter.Text != "" {
		tokens := search.AnalyzeAll(text)
		for _, tag := range tags {
			tokens = search.MergeTokens(tokens, search.Analyze(tag))
		}
		if !search.Matches(tokens, filter.Text) {
			return false
		}
	}
	if len(filter.Tags) > 0 && !hasAllTags(tags, filter.Tags) {
		return false
	}
	return true
}

func renderResult(ref search.Ref, snapshot domain.Snapshot) (SearchResult, bool) {
	result := SearchResult{Kind: ref.Kind, ID: ref.ID, Tags: []string{}}
	switch ref.Kind {
	case search.KindFormula:
		formula, ok := snapshot.Formulas[ref.ID]
		if !ok {
			return SearchResult{}, false
		}
		result.Label, result.State = formula.Name, string(formula.State)
		result.Tags = append(result.Tags, formula.Tags...)
		result.UpdatedAt = formula.UpdatedAt
		result.DetailURL = "/v1/formulas/" + formula.ID
	case search.KindVessel:
		vessel, ok := snapshot.Vessels[ref.ID]
		if !ok {
			return SearchResult{}, false
		}
		result.Label, result.State = vessel.Code, string(vessel.State)
		result.VesselCode = vessel.Code
		result.Tags = append(result.Tags, vessel.Tags...)
		result.UpdatedAt = vessel.UpdatedAt
		result.DetailURL = "/v1/vessels/" + vessel.ID
	case search.KindRun:
		run, ok := snapshot.Runs[ref.ID]
		if !ok {
			return SearchResult{}, false
		}
		result.Label, result.State = run.Code, string(run.State)
		result.Tags = append(result.Tags, run.Tags...)
		result.UpdatedAt = run.UpdatedAt
		result.DetailURL = "/v1/runs/" + run.ID
		if run.VesselID != nil {
			if vessel, found := snapshot.Vessels[*run.VesselID]; found {
				result.VesselCode = vessel.Code
			}
		}
	default:
		return SearchResult{}, false
	}
	return result, true
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func hasAllTags(actual, wanted []string) bool {
	set := make(map[string]struct{}, len(actual))
	for _, tag := range actual {
		set[tag] = struct{}{}
	}
	for _, tag := range wanted {
		if _, ok := set[tag]; !ok {
			return false
		}
	}
	return true
}

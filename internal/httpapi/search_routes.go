package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/search"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
)

const (
	defaultSearchLimit = 25
	maxSearchLimit     = 100
)

var (
	validSearchKinds = map[string]search.Kind{
		"formula": search.KindFormula,
		"vessel":  search.KindVessel,
		"run":     search.KindRun,
	}
	validSearchStates = map[string]map[string]struct{}{
		"formula": {
			string(domain.FormulaDraft):    {},
			string(domain.FormulaApproved): {},
			string(domain.FormulaRetired):  {},
		},
		"vessel": {
			string(domain.VesselAvailable): {},
			string(domain.VesselReserved):  {},
			string(domain.VesselInUse):     {},
			string(domain.VesselCleaning):  {},
			string(domain.VesselRetired):   {},
		},
		"run": {
			string(domain.RunPlanned):      {},
			string(domain.RunReserved):     {},
			string(domain.RunFermenting):   {},
			string(domain.RunConditioning): {},
			string(domain.RunCompleted):    {},
			string(domain.RunAborted):      {},
		},
	}
)

func registerSearchRoutes(mux *http.ServeMux, app *service.Service) {
	mux.HandleFunc("GET /v1/search", func(response http.ResponseWriter, request *http.Request) {
		query, err := parseSearchQuery(request)
		if err != nil {
			writeDomainError(response, err)
			return
		}
		page, err := app.SearchPage(query)
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, page)
	})

	mux.HandleFunc("POST /v1/search/rebuild", func(response http.ResponseWriter, _ *http.Request) {
		status, err := app.RebuildSearchIndex()
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{
			"rebuilt": true,
			"status":  status,
		})
	})

	mux.HandleFunc("GET /v1/search/status", func(response http.ResponseWriter, _ *http.Request) {
		status, err := app.SearchIndexStatus()
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, status)
	})

	mux.HandleFunc("GET /v1/search/tags", func(response http.ResponseWriter, _ *http.Request) {
		tags, err := app.SearchTags()
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"tags": tags})
	})
}

func parseSearchQuery(request *http.Request) (search.Query, error) {
	values := request.URL.Query()
	query := search.Query{
		Text:       strings.TrimSpace(values.Get("q")),
		VesselCode: strings.TrimSpace(values.Get("vessel_code")),
		Limit:      defaultSearchLimit,
		Cursor:     strings.TrimSpace(values.Get("cursor")),
	}

	for _, raw := range values["kind"] {
		for _, part := range strings.Split(raw, ",") {
			name := strings.TrimSpace(part)
			if name == "" {
				continue
			}
			kind, ok := validSearchKinds[name]
			if !ok {
				return search.Query{}, domain.FieldError(domain.CodeInvalidInput, "kind",
					"must be one of formula, vessel, run")
			}
			query.Kinds = append(query.Kinds, kind)
		}
	}
	query.Kinds = dedupeKinds(query.Kinds)

	requestedKinds := requestedKindSet(query.Kinds)
	for _, raw := range values["state"] {
		for _, part := range strings.Split(raw, ",") {
			state := strings.TrimSpace(part)
			if state == "" {
				continue
			}
			if !stateAllowedForKinds(state, requestedKinds) {
				return search.Query{}, domain.FieldError(domain.CodeInvalidInput, "state",
					"unknown state for the selected entity kinds")
			}
			query.States = append(query.States, state)
		}
	}
	query.States = dedupeStrings(query.States)
	query.Tags = values["tag"]

	if raw := strings.TrimSpace(values.Get("include_archived")); raw != "" {
		included, err := strconv.ParseBool(raw)
		if err != nil {
			return search.Query{}, domain.FieldError(domain.CodeInvalidInput,
				"include_archived", "must be true or false")
		}
		query.IncludeArchived = included
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > maxSearchLimit {
			return search.Query{}, domain.FieldError(domain.CodeInvalidInput, "limit",
				"must be an integer between 1 and 100")
		}
		query.Limit = limit
	}
	if len([]rune(query.Text)) > 200 {
		return search.Query{}, domain.FieldError(domain.CodeInvalidInput, "q",
			"must not exceed 200 characters")
	}
	if len(query.Tags) > 8 {
		return search.Query{}, domain.FieldError(domain.CodeInvalidInput, "tag",
			"at most 8 tag filters are allowed")
	}
	return query, nil
}

func requestedKindSet(kinds []search.Kind) map[string]struct{} {
	if len(kinds) == 0 {
		return map[string]struct{}{"formula": {}, "vessel": {}, "run": {}}
	}
	set := make(map[string]struct{}, len(kinds))
	for _, kind := range kinds {
		set[string(kind)] = struct{}{}
	}
	return set
}

func stateAllowedForKinds(state string, kinds map[string]struct{}) bool {
	for kind := range kinds {
		if _, ok := validSearchStates[kind][state]; ok {
			return true
		}
	}
	return false
}

func dedupeKinds(kinds []search.Kind) []search.Kind {
	seen := map[search.Kind]struct{}{}
	unique := make([]search.Kind, 0, len(kinds))
	for _, kind := range kinds {
		if _, ok := seen[kind]; ok {
			continue
		}
		seen[kind] = struct{}{}
		unique = append(unique, kind)
	}
	return unique
}

func dedupeStrings(values []string) []string {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

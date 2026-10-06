package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/search"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
)

func registerSearchRoutes(mux *http.ServeMux, app *service.Service) {
	mux.HandleFunc("GET /v1/search", func(response http.ResponseWriter, request *http.Request) {
		input, err := parseSearchQuery(request)
		if err != nil {
			writeDomainError(response, err)
			return
		}
		page, err := app.Search(input)
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, page)
	})
}

func parseSearchQuery(request *http.Request) (service.SearchInput, error) {
	values := request.URL.Query()
	input := service.SearchInput{
		Text:            strings.TrimSpace(values.Get("q")),
		VesselCode:      domain.NormalizeVesselCode(values.Get("vessel_code")),
		Cursor:          strings.TrimSpace(values.Get("cursor")),
		Sort:            search.SortLabel,
		IncludeArchived: values.Get("include_archived") == "true",
	}

	for _, kind := range values["kind"] {
		for _, piece := range strings.Split(kind, ",") {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			switch search.Kind(piece) {
			case search.KindFormula, search.KindVessel, search.KindRun:
				input.Kinds = append(input.Kinds, search.Kind(piece))
			default:
				return input, domain.FieldError(domain.CodeInvalidInput, "kind",
					"must be one of formula, vessel, run")
			}
		}
	}

	knownStates := map[string]struct{}{
		"draft": {}, "approved": {}, "retired": {},
		"available": {}, "reserved": {}, "in_use": {}, "cleaning": {},
		"planned": {}, "fermenting": {}, "conditioning": {},
		"completed": {}, "aborted": {},
	}
	for _, state := range values["state"] {
		for _, piece := range strings.Split(state, ",") {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			if _, ok := knownStates[piece]; !ok {
				return input, domain.FieldError(domain.CodeInvalidInput, "state",
					"is not a recognized lifecycle state")
			}
			input.States = append(input.States, piece)
		}
	}

	if rawTags, ok := values["tag"]; ok {
		input.Tags = rawTags
	}

	switch sortValue := values.Get("sort"); sortValue {
	case "", "label":
		input.Sort = search.SortLabel
	case "updated":
		input.Sort = search.SortUpdated
	default:
		return input, domain.FieldError(domain.CodeInvalidInput, "sort",
			"must be label or updated")
	}

	switch order := values.Get("order"); order {
	case "", "asc":
		input.Desc = false
	case "desc":
		input.Desc = true
	default:
		return input, domain.FieldError(domain.CodeInvalidInput, "order",
			"must be asc or desc")
	}

	if rawLimit := values.Get("limit"); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil || limit < 1 || limit > search.MaxLimit {
			return input, domain.FieldError(domain.CodeInvalidInput, "limit",
				"must be between 1 and 100")
		}
		input.Limit = limit
	}
	return input, nil
}

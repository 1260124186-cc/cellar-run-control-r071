package workflowcheck

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

type searchPageView struct {
	Results []struct {
		Ref struct {
			Kind string `json:"kind"`
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"ref"`
		Label      string   `json:"label"`
		State      string   `json:"state"`
		Tags       []string `json:"tags"`
		VesselCode string   `json:"vessel_code"`
		FormulaID  string   `json:"formula_id"`
	} `json:"results"`
	NextCursor   string `json:"next_cursor"`
	HasMore      bool   `json:"has_more"`
	Generation   int64  `json:"generation"`
	LiveRevision int64  `json:"live_revision"`
}

type searchStatusView struct {
	Generation    int64 `json:"generation"`
	SourceRev     int64 `json:"source_revision"`
	DocumentCount int   `json:"document_count"`
	LiveRevision  int64 `json:"live_revision"`
	CaughtUp      bool  `json:"caught_up"`
}

func searchIndex(api *client) error {
	approved, err := createApprovedFormula(api, "Search Stout", "High Gravity Imperial",
		[]string{"high-gravity", "barrel"})
	if err != nil {
		return err
	}
	if _, err := createApprovedFormula(api, "Search Pale", "Crisp Pale",
		[]string{"session"}); err != nil {
		return err
	}

	vesselA, err := createVesselTagged(api, "FV-100", 500, []string{"big", "stainless"})
	if err != nil {
		return err
	}
	vesselB, err := createVesselTagged(api, "FV-200", 200, []string{"small"})
	if err != nil {
		return err
	}

	runOne, err := createRunTagged(api, "RUN-ALPHA-1", approved.ID, []string{"barrel"})
	if err != nil {
		return err
	}
	if _, err := createRunTagged(api, "RUN-BETA-2", approved.ID, []string{"experimental"}); err != nil {
		return err
	}

	if err := waitForCaughtUp(api); err != nil {
		return err
	}

	if err := assertTextSearch(api); err != nil {
		return err
	}
	if err := assertKindStateFilters(api); err != nil {
		return err
	}
	if err := assertVesselAndTagFilters(api, vesselA.ID, vesselB.ID, runOne.ID); err != nil {
		return err
	}
	if err := assertCursorPagination(api); err != nil {
		return err
	}
	if err := assertArchivalHidesResults(api, runOne.ID); err != nil {
		return err
	}
	if err := assertRebuildAndDetail(api); err != nil {
		return err
	}
	if err := assertBadInputs(api); err != nil {
		return err
	}
	return nil
}

func createApprovedFormula(api *client, name, style string, tags []string) (formulaView, error) {
	payload := map[string]any{
		"name":                    name,
		"style":                   style,
		"target_original_gravity": 1.080,
		"target_final_gravity":    1.016,
		"max_fermentation_temp_c": 22.0,
		"minimum_days":            14,
		"maximum_days":            60,
		"tags":                    tags,
	}
	created, err := api.call("POST", "/v1/formulas", payload, nil)
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(created, 201, "create search formula"); err != nil {
		return formulaView{}, err
	}
	var formula formulaView
	if err := created.decode(&formula); err != nil {
		return formulaView{}, err
	}
	steps, err := api.call("PUT", "/v1/formulas/"+formula.ID+"/steps",
		formulaStepPayload(), ptrInt64(formula.Version))
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(steps, 200, "set search formula steps"); err != nil {
		return formulaView{}, err
	}
	if err := steps.decode(&formula); err != nil {
		return formulaView{}, err
	}
	approved, err := api.call("POST", "/v1/formulas/"+formula.ID+"/approve", nil,
		ptrInt64(formula.Version))
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(approved, 200, "approve search formula"); err != nil {
		return formulaView{}, err
	}
	if err := approved.decode(&formula); err != nil {
		return formulaView{}, err
	}
	return formula, nil
}

func createVesselTagged(api *client, code string, capacity float64, tags []string) (vesselView, error) {
	response, err := api.call("POST", "/v1/vessels", map[string]any{
		"code": code, "capacity_liters": capacity, "tags": tags,
	}, nil)
	if err != nil {
		return vesselView{}, err
	}
	if err := requireStatus(response, 201, "create search vessel"); err != nil {
		return vesselView{}, err
	}
	var vessel vesselView
	if err := response.decode(&vessel); err != nil {
		return vesselView{}, err
	}
	return vessel, nil
}

func createRunTagged(api *client, code, formulaID string, tags []string) (runView, error) {
	response, err := api.call("POST", "/v1/runs", map[string]any{
		"code":                  code,
		"formula_id":            formulaID,
		"planned_volume_liters": 100.0,
		"tags":                  tags,
	}, nil)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 201, "create search run"); err != nil {
		return runView{}, err
	}
	var run runView
	if err := response.decode(&run); err != nil {
		return runView{}, err
	}
	return run, nil
}

func waitForCaughtUp(api *client) error {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		response, err := api.call("GET", "/v1/search/status", nil, nil)
		if err != nil {
			return err
		}
		if response.status != 200 {
			return fmt.Errorf("search status returned %d", response.status)
		}
		var status searchStatusView
		if err := response.decode(&status); err != nil {
			return err
		}
		if status.CaughtUp && status.DocumentCount >= 6 {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("search index did not catch up with the committed snapshot")
}

func getSearch(api *client, rawQuery string) (searchPageView, error) {
	response, err := api.call("GET", "/v1/search?"+rawQuery, nil, nil)
	if err != nil {
		return searchPageView{}, err
	}
	if err := requireStatus(response, 200, "search: "+rawQuery); err != nil {
		return searchPageView{}, err
	}
	var page searchPageView
	if err := response.decode(&page); err != nil {
		return searchPageView{}, err
	}
	return page, nil
}

func assertTextSearch(api *client) error {
	// Free text reaches formula name and style.
	page, err := getSearch(api, url.Values{"q": {"stout"}}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 1 || page.Results[0].Ref.Kind != "formula" {
		return fmt.Errorf("name search did not return the single stout formula")
	}
	if page.Results[0].Ref.Path != "/v1/formulas/"+page.Results[0].Ref.ID {
		return fmt.Errorf("hit does not link back to its detail path")
	}

	// Style words match too, including the "high gravity" style description.
	page, err = getSearch(api, url.Values{"q": {"imperial"}}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 1 || page.Results[0].Label != "Search Stout" {
		return fmt.Errorf("style search did not find the imperial formula")
	}

	// Dashed codes match with or without the dash.
	for _, term := range []string{"fv100", "FV-100"} {
		page, err = getSearch(api, url.Values{"q": {term}}.Encode())
		if err != nil {
			return err
		}
		if len(page.Results) != 1 || page.Results[0].Ref.Kind != "vessel" {
			return fmt.Errorf("code search %q did not return exactly FV-100", term)
		}
	}
	page, err = getSearch(api, url.Values{"q": {"run-alpha"}}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 1 || page.Results[0].Ref.Kind != "run" ||
		page.Results[0].Label != "RUN-ALPHA-1" {
		return fmt.Errorf("code search run-alpha did not return exactly RUN-ALPHA-1")
	}

	// Tag text is searchable too: "stainless" only labels FV-100.
	page, err = getSearch(api, url.Values{"q": {"stainless"}}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 1 || page.Results[0].Ref.Kind != "vessel" ||
		page.Results[0].Label != "FV-100" {
		return fmt.Errorf("free-text tag search did not isolate FV-100")
	}

	// Unknown text matches nothing.
	page, err = getSearch(api, url.Values{"q": {"zzz-no-such-thing"}}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 0 || page.HasMore {
		return fmt.Errorf("search for nonsense returned results")
	}
	return nil
}

func assertKindStateFilters(api *client) error {
	page, err := getSearch(api, url.Values{"kind": {"vessel"}}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 2 {
		return fmt.Errorf("vessel kind filter returned %d hits", len(page.Results))
	}
	for _, hit := range page.Results {
		if hit.Ref.Kind != "vessel" {
			return fmt.Errorf("kind filter leaked a non-vessel result")
		}
	}

	page, err = getSearch(api, url.Values{
		"kind": {"run"}, "state": {"planned"},
	}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 2 {
		return fmt.Errorf("planned run filter returned %d hits, want 2", len(page.Results))
	}

	// Kind and state can be narrowed simultaneously.
	page, err = getSearch(api, url.Values{
		"kind": {"formula"}, "state": {"approved"},
	}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 2 {
		return fmt.Errorf("approved formula filter returned %d hits, want 2", len(page.Results))
	}
	return nil
}

func assertVesselAndTagFilters(api *client, vesselA, vesselB, runOne string) error {
	// Reserve vessel A for the first run so its live vessel code is FV-100.
	response, err := api.call("POST", "/v1/runs/"+runOne+"/reserve",
		map[string]any{"vessel_id": vesselA}, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(response, 200, "reserve search run"); err != nil {
		return err
	}
	if err := waitForCaughtUp(api); err != nil {
		return err
	}

	page, err := getSearch(api, url.Values{
		"kind": {"run"}, "vessel_code": {"FV-100"},
	}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 1 || page.Results[0].Ref.ID != runOne {
		return fmt.Errorf("vessel_code filter did not isolate the run on FV-100")
	}
	if page.Results[0].VesselCode != "FV-100" {
		return fmt.Errorf("run hit carried the wrong vessel code %q", page.Results[0].VesselCode)
	}

	// The second vessel must never be mixed into the first run's facts.
	page, err = getSearch(api, url.Values{
		"kind": {"run"}, "vessel_code": {"FV-200"},
	}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 0 {
		return fmt.Errorf("FV-200 filter leaked a run from another vessel")
	}

	// Tags are AND-filtered across kinds.
	page, err = getSearch(api, url.Values{"tag": {"barrel"}}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 2 { // stout formula + alpha run
		return fmt.Errorf("barrel tag returned %d hits, want 2", len(page.Results))
	}
	for _, hit := range page.Results {
		if !contains(hit.Tags, "barrel") {
			return fmt.Errorf("tag filter returned an untagged result")
		}
	}

	page, err = getSearch(api, url.Values{"tag": {"barrel", "experimental"}}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 0 {
		return fmt.Errorf("AND tag filters must require every tag on one entity")
	}

	// Tag suggestion endpoint reflects the live snapshot.
	suggestions, err := api.call("GET", "/v1/search/tags", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(suggestions, 200, "list search tags"); err != nil {
		return err
	}
	var tagList struct {
		Tags []string `json:"tags"`
	}
	if err := suggestions.decode(&tagList); err != nil {
		return err
	}
	if !contains(tagList.Tags, "high-gravity") {
		return fmt.Errorf("tag suggestions are missing high-gravity")
	}
	return nil
}

func assertCursorPagination(api *client) error {
	first, err := getSearch(api, url.Values{"limit": {"3"}}.Encode())
	if err != nil {
		return err
	}
	if !first.HasMore || first.NextCursor == "" || len(first.Results) != 3 {
		return fmt.Errorf("first page did not advertise a cursor")
	}
	firstGeneration := first.Generation
	seen := map[string]struct{}{}
	for _, hit := range first.Results {
		seen[hit.Ref.Kind+"/"+hit.Ref.ID] = struct{}{}
	}

	cursor := first.NextCursor
	pages := 1
	total := len(first.Results)
	for {
		page, err := getSearch(api, url.Values{"limit": {"3"}, "cursor": {cursor}}.Encode())
		if err != nil {
			return err
		}
		if page.Generation != firstGeneration {
			return fmt.Errorf("cursor paging straddled index generations")
		}
		for _, hit := range page.Results {
			key := hit.Ref.Kind + "/" + hit.Ref.ID
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("cursor pagination repeated %s", key)
			}
			seen[key] = struct{}{}
		}
		total += len(page.Results)
		pages++
		if !page.HasMore {
			break
		}
		cursor = page.NextCursor
		if pages > 10 {
			return fmt.Errorf("pagination did not terminate")
		}
	}
	if total < 6 {
		return fmt.Errorf("cursor walk covered %d hits, want at least 6", total)
	}

	// Malformed and forged cursors are rejected with stable codes.
	bad, err := api.call("GET", "/v1/search?cursor=not-base64!", nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(bad, 400, string(domain.CodeSearchCursor), "bad cursor"); err != nil {
		return err
	}
	forged, _ := json.Marshal(struct {
		S int64 `json:"s"`
		K struct {
			K string `json:"k"`
			I string `json:"i"`
		} `json:"k"`
	}{S: 999999, K: struct {
		K string `json:"k"`
		I string `json:"i"`
	}{K: "vessel", I: "vessel_x"}})
	encoded := encodeURLCursor(forged)
	stale, err := api.call("GET", "/v1/search?cursor="+encoded, nil, nil)
	if err != nil {
		return err
	}
	if err := requireCode(stale, 410, string(domain.CodeSearchStale), "expired cursor"); err != nil {
		return err
	}
	return nil
}

func encodeURLCursor(raw []byte) string {
	return url.QueryEscape(base64.RawURLEncoding.EncodeToString(raw))
}

func assertArchivalHidesResults(api *client, runOne string) error {
	// Aborted runs are archived and must disappear from default searches.
	aborted, err := api.call("POST", "/v1/runs/"+runOne+"/abort", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(aborted, 200, "abort search run"); err != nil {
		return err
	}
	if err := waitForCaughtUp(api); err != nil {
		return err
	}

	page, err := getSearch(api, url.Values{"q": {"RUN-ALPHA-1"}}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 0 {
		return fmt.Errorf("aborted run leaked into default search results")
	}

	page, err = getSearch(api, url.Values{
		"q": {"RUN-ALPHA-1"}, "include_archived": {"true"},
	}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 1 || page.Results[0].State != "aborted" {
		return fmt.Errorf("include_archived did not surface the aborted run")
	}

	// Retiring the vessel hides it too.
	var vessel struct {
		ID      string `json:"id"`
		State   string `json:"state"`
		Version int64  `json:"version"`
	}
	finish, err := api.call("POST", "/v1/vessels", map[string]any{
		"code": "FV-RETIRE", "capacity_liters": 50,
	}, nil)
	if err != nil {
		return err
	}
	if err := finish.decode(&vessel); err != nil {
		return err
	}
	retired, err := api.call("POST", "/v1/vessels/"+vessel.ID+"/retire", nil, ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireStatus(retired, 200, "retire search vessel"); err != nil {
		return err
	}
	if err := waitForCaughtUp(api); err != nil {
		return err
	}
	page, err = getSearch(api, url.Values{"q": {"FV-RETIRE"}}.Encode())
	if err != nil {
		return err
	}
	if len(page.Results) != 0 {
		return fmt.Errorf("retired vessel leaked into default search results")
	}
	return nil
}

func assertRebuildAndDetail(api *client) error {
	before, err := api.call("GET", "/v1/search/status", nil, nil)
	if err != nil {
		return err
	}
	var status searchStatusView
	if err := before.decode(&status); err != nil {
		return err
	}

	rebuilt, err := api.call("POST", "/v1/search/rebuild", nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(rebuilt, 200, "rebuild search index"); err != nil {
		return err
	}
	var rebuildBody struct {
		Status searchStatusView `json:"status"`
	}
	if err := rebuilt.decode(&rebuildBody); err != nil {
		return err
	}
	if rebuildBody.Status.Generation <= status.Generation {
		return fmt.Errorf("rebuild did not publish a newer generation")
	}
	if rebuildBody.Status.DocumentCount == 0 {
		return fmt.Errorf("rebuilt index is empty")
	}

	// Every hit must resolve to an existing detail endpoint.
	page, err := getSearch(api, "")
	if err != nil {
		return err
	}
	if len(page.Results) == 0 {
		return fmt.Errorf("full search returned no results after rebuild")
	}
	for _, hit := range page.Results {
		detail, err := api.call("GET", hit.Ref.Path, nil, nil)
		if err != nil {
			return err
		}
		if err := requireStatus(detail, 200, "resolve hit detail "+hit.Ref.Path); err != nil {
			return err
		}
	}

	// The live vessel hit keeps its post-abort cleaning state from the snapshot.
	vesselPage, err := getSearch(api, url.Values{
		"kind": {"vessel"}, "q": {"FV-100"}, "include_archived": {"true"},
	}.Encode())
	if err != nil {
		return err
	}
	if len(vesselPage.Results) != 1 {
		return fmt.Errorf("expected FV-100 to remain searchable as a vessel")
	}
	if vesselPage.Results[0].State != "available" {
		return fmt.Errorf("vessel hit shows stale state %q, want available after pre-fermentation abort",
			vesselPage.Results[0].State)
	}
	return nil
}

func assertBadInputs(api *client) error {
	cases := []struct {
		path string
		code string
	}{
		{"/v1/search?kind=widget", string(domain.CodeInvalidInput)},
		{"/v1/search?state=frozen", string(domain.CodeInvalidInput)},
		{"/v1/search?limit=0", string(domain.CodeInvalidInput)},
		{"/v1/search?limit=9999", string(domain.CodeInvalidInput)},
		{"/v1/search?include_archived=maybe", string(domain.CodeInvalidInput)},
	}
	for _, tc := range cases {
		response, err := api.call("GET", tc.path, nil, nil)
		if err != nil {
			return err
		}
		if err := requireCode(response, 400, tc.code, "bad input "+tc.path); err != nil {
			return err
		}
	}

	// Malformed tags on creation are rejected.
	response, err := api.call("POST", "/v1/vessels", map[string]any{
		"code": "FV-BADTAG", "capacity_liters": 50, "tags": []string{"UPPER CASE"},
	}, nil)
	if err != nil {
		return err
	}
	if err := requireCode(response, 400, string(domain.CodeInvalidInput), "bad tag shape"); err != nil {
		return err
	}
	return nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if strings.EqualFold(value, wanted) {
			return true
		}
	}
	return false
}

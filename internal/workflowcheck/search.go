package workflowcheck

import (
	"encoding/json"
	"fmt"
	"net/url"
	"time"
)

type searchResultView struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Label      string `json:"label"`
	State      string `json:"state"`
	VesselCode string `json:"vessel_code"`
	DetailURL  string `json:"detail_url"`
}

type searchPageView struct {
	Results    []searchResultView `json:"results"`
	NextCursor string             `json:"next_cursor"`
	Revision   int64              `json:"revision"`
}

func searchIndex(api *client) error {
	formulaA, err := ensureApprovedFormulaWithTags(api, "高度数博克", "Strong Bock",
		[]string{"高比重", "barrel-a1"})
	if err != nil {
		return err
	}
	formulaB, err := ensureApprovedFormulaWithTags(api, "上周那桶高度数配方", "Imperial Stout",
		[]string{"高比重"})
	if err != nil {
		return err
	}
	formulaC, err := ensureApprovedFormulaWithTags(api, "清爽小麦", "Wheat", nil)
	if err != nil {
		return err
	}

	vessel301, err := createCheckVessel(api, "VSL-301", 400)
	if err != nil {
		return err
	}
	vessel302, err := createCheckVessel(api, "VSL-302", 400)
	if err != nil {
		return err
	}
	vessel900, err := createCheckVessel(api, "VSL-900", 400)
	if err != nil {
		return err
	}

	run1, err := createReservedStartedRun(api, formulaA.ID, vessel301.ID, "RUN-SRC-1")
	if err != nil {
		return err
	}
	if _, err := createReservedRun(api, formulaA.ID, vessel302.ID, "RUN-SRC-2"); err != nil {
		return err
	}
	abortedRun, err := createPlannedRun(api, formulaA.ID, "RUN-SRC-ABORT")
	if err != nil {
		return err
	}

	// 1. Chinese name and style terms are tokenized without a dictionary.
	highGravity, err := waitForSearch(api, url.Values{"q": {"高度数"}, "kind": {"formula"}},
		func(page searchPageView) bool { return len(page.Results) >= 2 })
	if err != nil {
		return err
	}
	if err := requireIDs(highGravity.Results, formulaA.ID, formulaB.ID); err != nil {
		return err
	}
	for _, result := range highGravity.Results {
		if result.Kind != "formula" {
			return errInvalidWorkflow("formula query returned a non-formula kind")
		}
	}

	// 2. Prefix code search spans vessels and the runs that occupy them.
	byCode, err := waitForSearch(api, url.Values{"q": {"vsl-30"}},
		func(page searchPageView) bool { return len(page.Results) >= 4 })
	if err != nil {
		return err
	}
	if err := requireKindsAndIDs(byCode.Results,
		"vessel", vessel301.ID, "vessel", vessel302.ID,
		"run", run1.ID); err != nil {
		return err
	}

	// 3. Vessel code and run state narrow together, and never cross vessels.
	on301, err := waitForSearch(api, url.Values{
		"kind": {"run"}, "vessel_code": {"VSL-301"}, "state": {"fermenting"},
	}, func(page searchPageView) bool { return len(page.Results) == 1 })
	if err != nil {
		return err
	}
	if len(on301.Results) != 1 || on301.Results[0].ID != run1.ID ||
		on301.Results[0].VesselCode != "VSL-301" {
		return errInvalidWorkflow("vessel/state filter returned the wrong run or facts")
	}

	on302, err := waitForSearch(api, url.Values{
		"vessel_code": {"VSL-302"},
	}, func(page searchPageView) bool { return len(page.Results) >= 2 })
	if err != nil {
		return err
	}
	for _, result := range on302.Results {
		if result.VesselCode != "VSL-302" {
			return errInvalidWorkflow("a result carried another vessel's code")
		}
	}

	// 4. Structured tag filtering.
	tagged, err := waitForSearch(api, url.Values{
		"kind": {"formula"}, "tag": {"高比重"},
	}, func(page searchPageView) bool { return len(page.Results) == 2 })
	if err != nil {
		return err
	}
	if err := requireIDs(tagged.Results, formulaA.ID, formulaB.ID); err != nil {
		return err
	}

	// 5. Keyset pagination walks every formula once in stable label order.
	page1, err := searchNow(api, url.Values{"kind": {"formula"}, "limit": {"2"}})
	if err != nil {
		return err
	}
	if len(page1.Results) != 2 || page1.NextCursor == "" {
		return errInvalidWorkflow("first search page was not full or lacked a cursor")
	}
	page2, err := searchNow(api, url.Values{
		"kind": {"formula"}, "limit": {"2"}, "cursor": {page1.NextCursor},
	})
	if err != nil {
		return err
	}
	if len(page2.Results) != 1 || page2.NextCursor != "" {
		return errInvalidWorkflow("second search page had an unexpected shape")
	}
	all := append(append([]searchResultView{}, page1.Results...), page2.Results...)
	if len(all) != 3 {
		return errInvalidWorkflow("pagination did not walk every formula exactly once")
	}
	if err := requireIDs(all, formulaA.ID, formulaB.ID, formulaC.ID); err != nil {
		return err
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Label > all[i].Label {
			return errInvalidWorkflow("search ordering was not stable across pages")
		}
	}
	seen := map[string]bool{}
	for _, result := range all {
		if seen[result.ID] {
			return errInvalidWorkflow("pagination duplicated a formula")
		}
		seen[result.ID] = true
	}

	// A cursor bound to label order must not be replayed on updated order.
	mismatched, err := api.call("GET",
		"/v1/search?"+url.Values{
			"kind": {"formula"}, "sort": {"updated"}, "cursor": {page1.NextCursor},
		}.Encode(), nil, nil)
	if err != nil {
		return err
	}
	if mismatched.errorCode() != "invalid_input" {
		return errInvalidWorkflow("an incompatible cursor was not rejected")
	}

	// 6. Each result jumps back to its authoritative detail record.
	target := page1.Results[0]
	detail, err := api.call("GET", target.DetailURL, nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(detail, 200, "open search result detail"); err != nil {
		return err
	}
	var identity struct {
		ID string `json:"id"`
	}
	if err := detail.decode(&identity); err != nil || identity.ID != target.ID {
		return errInvalidWorkflow("detail link did not return the searched entity")
	}

	// 7. A retired vessel is excluded by default even before the asynchronous
	//    rebuild lands, because results are re-admitted against current truth.
	retireResponse, err := api.call("POST", "/v1/vessels/"+vessel900.ID+"/retire",
		nil, ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireStatus(retireResponse, 200, "retire search vessel"); err != nil {
		return err
	}
	hidden, err := searchNow(api, url.Values{"kind": {"vessel"}, "q": {"vsl-900"}})
	if err != nil {
		return err
	}
	if len(hidden.Results) != 0 {
		return errInvalidWorkflow("a retired vessel leaked into default results")
	}
	archived, err := waitForSearch(api, url.Values{
		"kind": {"vessel"}, "q": {"vsl-900"}, "include_archived": {"true"},
	}, func(page searchPageView) bool { return len(page.Results) == 1 })
	if err != nil {
		return err
	}
	if archived.Results[0].ID != vessel900.ID {
		return errInvalidWorkflow("archived vessel was not retrievable on demand")
	}

	// 8. An aborted run is archived by default but recoverable explicitly.
	abortResponse, err := api.call("POST", "/v1/runs/"+abortedRun.ID+"/abort",
		nil, ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireStatus(abortResponse, 200, "abort search run"); err != nil {
		return err
	}
	hiddenRun, err := searchNow(api, url.Values{"kind": {"run"}, "q": {"RUN-SRC-ABORT"}})
	if err != nil {
		return err
	}
	if len(hiddenRun.Results) != 0 {
		return errInvalidWorkflow("an aborted run leaked into default results")
	}
	archivedRun, err := waitForSearch(api, url.Values{
		"kind": {"run"}, "q": {"RUN-SRC-ABORT"}, "include_archived": {"true"},
	}, func(page searchPageView) bool { return len(page.Results) == 1 })
	if err != nil {
		return err
	}
	if archivedRun.Results[0].ID != abortedRun.ID {
		return errInvalidWorkflow("aborted run was not recoverable from the archive view")
	}
	return nil
}

func searchNow(api *client, values url.Values) (searchPageView, error) {
	response, err := api.call("GET", "/v1/search?"+values.Encode(), nil, nil)
	if err != nil {
		return searchPageView{}, err
	}
	if err := requireStatus(response, 200, "execute search"); err != nil {
		return searchPageView{}, err
	}
	var page searchPageView
	if err := response.decode(&page); err != nil {
		return searchPageView{}, err
	}
	return page, nil
}

// waitForSearch polls a query until the index generation catches up with the
// writes and the predicate holds. It tolerates the brief "index still
// building" window immediately after startup.
func waitForSearch(
	api *client,
	values url.Values,
	predicate func(searchPageView) bool,
) (searchPageView, error) {
	deadline := time.Now().Add(5 * time.Second)
	var last searchPageView
	for time.Now().Before(deadline) {
		response, err := api.call("GET", "/v1/search?"+values.Encode(), nil, nil)
		if err == nil && response.status == 200 {
			if err := json.Unmarshal(response.body, &last); err == nil && predicate(last) {
				return last, nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return last, errInvalidWorkflow("search did not converge to the expected results")
}

func requireIDs(results []searchResultView, ids ...string) error {
	wanted := map[string]bool{}
	for _, id := range ids {
		wanted[id] = false
	}
	for _, result := range results {
		if _, ok := wanted[result.ID]; ok {
			wanted[result.ID] = true
		}
	}
	for id, found := range wanted {
		if !found {
			return fmt.Errorf("search results did not contain %s", id)
		}
	}
	return nil
}

func requireKindsAndIDs(results []searchResultView, pairs ...string) error {
	if len(pairs)%2 != 0 {
		return fmt.Errorf("requireKindsAndIDs needs kind/id pairs")
	}
	type pair struct{ kind, id string }
	wanted := map[pair]bool{}
	for i := 0; i < len(pairs); i += 2 {
		wanted[pair{pairs[i], pairs[i+1]}] = false
	}
	for _, result := range results {
		key := pair{result.Kind, result.ID}
		if _, ok := wanted[key]; ok {
			wanted[key] = true
		}
	}
	for key, found := range wanted {
		if !found {
			return fmt.Errorf("search results did not contain %s/%s", key.kind, key.id)
		}
	}
	return nil
}

func createFormulaPayloadTags(name, style string, tags []string) map[string]any {
	payload := map[string]any{
		"name":                    name,
		"style":                   style,
		"target_original_gravity": 1.072,
		"target_final_gravity":    1.016,
		"max_fermentation_temp_c": 22.0,
		"minimum_days":            7,
		"maximum_days":            30,
	}
	if tags != nil {
		payload["tags"] = tags
	}
	return payload
}

func ensureApprovedFormulaWithTags(
	api *client,
	name, style string,
	tags []string,
) (formulaView, error) {
	createdResponse, err := api.call("POST", "/v1/formulas",
		createFormulaPayloadTags(name, style, tags), nil)
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(createdResponse, 201, "create tagged formula"); err != nil {
		return formulaView{}, err
	}
	var formula formulaView
	if err := createdResponse.decode(&formula); err != nil {
		return formulaView{}, err
	}
	stepsResponse, err := api.call("PUT", "/v1/formulas/"+formula.ID+"/steps",
		formulaStepPayload(), ptrInt64(1))
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(stepsResponse, 200, "set tagged formula steps"); err != nil {
		return formulaView{}, err
	}
	approveResponse, err := api.call("POST", "/v1/formulas/"+formula.ID+"/approve",
		nil, ptrInt64(2))
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(approveResponse, 200, "approve tagged formula"); err != nil {
		return formulaView{}, err
	}
	if err := approveResponse.decode(&formula); err != nil {
		return formulaView{}, err
	}
	return formula, nil
}

func createPlannedRun(api *client, formulaID, code string) (runView, error) {
	response, err := api.call("POST", "/v1/runs",
		createRunPayload(code, formulaID), nil)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 201, "create planned search run"); err != nil {
		return runView{}, err
	}
	var run runView
	if err := response.decode(&run); err != nil {
		return runView{}, err
	}
	return run, nil
}

func createReservedRun(api *client, formulaID, vesselID, code string) (pairView, error) {
	run, err := createPlannedRun(api, formulaID, code)
	if err != nil {
		return pairView{}, err
	}
	response, err := api.call("POST", "/v1/runs/"+run.ID+"/reserve",
		map[string]any{"vessel_id": vesselID}, ptrInt64(run.Version))
	if err != nil {
		return pairView{}, err
	}
	if err := requireStatus(response, 200, "reserve search run"); err != nil {
		return pairView{}, err
	}
	var pair pairView
	if err := response.decode(&pair); err != nil {
		return pairView{}, err
	}
	return pair, nil
}

func createReservedStartedRun(
	api *client,
	formulaID, vesselID, code string,
) (runView, error) {
	reserved, err := createReservedRun(api, formulaID, vesselID, code)
	if err != nil {
		return runView{}, err
	}
	response, err := api.call("POST", "/v1/runs/"+reserved.Run.ID+"/start",
		nil, ptrInt64(reserved.Run.Version))
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 200, "start search run"); err != nil {
		return runView{}, err
	}
	var pair pairView
	if err := response.decode(&pair); err != nil {
		return runView{}, err
	}
	return pair.Run, nil
}

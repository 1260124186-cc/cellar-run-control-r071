package search

import "github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"

// DocumentsFromSnapshot projects an authoritative snapshot into searchable
// documents. Every document carries its own (kind, id) and the facts needed
// for coarse filtering; the snapshot remains the single source of truth for
// whatever a caller eventually renders.
func DocumentsFromSnapshot(snapshot domain.Snapshot) []Document {
	documents := make([]Document, 0,
		len(snapshot.Formulas)+len(snapshot.Vessels)+len(snapshot.Runs))

	vesselCodes := make(map[string]string, len(snapshot.Vessels))
	for id, vessel := range snapshot.Vessels {
		vesselCodes[id] = vessel.Code
	}

	for id, formula := range snapshot.Formulas {
		documents = append(documents, Document{
			Ref:       Ref{Kind: KindFormula, ID: id},
			Label:     formula.Name,
			State:     string(formula.State),
			Tags:      formula.Tags,
			Text:      []string{formula.Name, formula.Style},
			UpdatedAt: formula.UpdatedAt,
		})
	}
	for id, vessel := range snapshot.Vessels {
		documents = append(documents, Document{
			Ref:        Ref{Kind: KindVessel, ID: id},
			Label:      vessel.Code,
			VesselCode: vessel.Code,
			State:      string(vessel.State),
			Tags:       vessel.Tags,
			Text:       []string{vessel.Code},
			UpdatedAt:  vessel.UpdatedAt,
		})
	}
	for id, run := range snapshot.Runs {
		text := []string{run.Code, run.Notes}
		document := Document{
			Ref:       Ref{Kind: KindRun, ID: id},
			Label:     run.Code,
			State:     string(run.State),
			Tags:      run.Tags,
			UpdatedAt: run.UpdatedAt,
		}
		if run.VesselID != nil {
			// Index the joined vessel code and id as build-time hints. A stale
			// generation may carry an association that has since changed; the
			// authoritative admit callback resolves it again before surfacing
			// this run.
			if code, ok := vesselCodes[*run.VesselID]; ok && code != "" {
				document.VesselCode = code
				text = append(text, code)
			}
		}
		document.Text = text
		documents = append(documents, document)
	}
	return documents
}

package search

// Query is the parsed and validated search request. Text terms must all match
// (AND), as must every supplied tag. Filters other than text are applied to
// the live snapshot during resolution, not to the stored generation.
type Query struct {
	Text            string
	Kinds           []Kind
	States          []string
	VesselCode      string
	Tags            []string
	IncludeArchived bool
	Limit           int
	Cursor          string
}

// Ref is the stable pointer that lets a client jump straight to the detail
// endpoint of a hit.
type Ref struct {
	Kind Kind   `json:"kind"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Result is one search hit. Every field except the search coordinates is
// populated from the live snapshot at query time.
type Result struct {
	Ref        Ref      `json:"ref"`
	Label      string   `json:"label"`
	Detail     string   `json:"detail"`
	State      string   `json:"state"`
	Tags       []string `json:"tags"`
	UpdatedAt  string   `json:"updated_at"`
	VesselID   string   `json:"vessel_id,omitempty"`
	VesselCode string   `json:"vessel_code,omitempty"`
	FormulaID  string   `json:"formula_id,omitempty"`
}

// Page is one stable slice of results plus the continuation cursor.
type Page struct {
	Results      []Result `json:"results"`
	NextCursor   string   `json:"next_cursor,omitempty"`
	HasMore      bool     `json:"has_more"`
	Generation   int64    `json:"generation"`
	LiveRevision int64    `json:"live_revision"`
}

package search

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

// cursorKey is the total-order position of a document within one generation.
// The sort is: most recently updated first, then kind rank, then entity ID.
type cursorKey struct {
	UpdatedAt time.Time `json:"u"`
	Kind      Kind      `json:"k"`
	ID        string    `json:"i"`
}

// Cursor is the opaque continuation token handed to clients. It pins the
// generation the page belongs to so pagination cannot straddle two indexes.
type Cursor struct {
	Serial int64     `json:"s"`
	Key    cursorKey `json:"k"`
}

func encodeCursor(cursor Cursor) string {
	raw, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// DecodeCursor parses an opaque continuation token.
func DecodeCursor(value string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return Cursor{}, domain.NewError(domain.CodeSearchCursor, "cursor is not valid base64")
	}
	var cursor Cursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return Cursor{}, domain.NewError(domain.CodeSearchCursor, "cursor payload is malformed")
	}
	if cursor.Serial < 1 || cursor.Key.ID == "" {
		return Cursor{}, domain.NewError(domain.CodeSearchCursor, "cursor payload is incomplete")
	}
	return cursor, nil
}

// compareOrder returns true when candidate sorts strictly after the given key
// (i.e. the candidate appears later in descending updated_at order).
func compareOrder(candidate cursorKey, after cursorKey) bool {
	if !candidate.UpdatedAt.Equal(after.UpdatedAt) {
		return candidate.UpdatedAt.After(after.UpdatedAt)
	}
	if candidate.Kind != after.Kind {
		return kindRank(candidate.Kind) < kindRank(after.Kind)
	}
	return candidate.ID > after.ID
}

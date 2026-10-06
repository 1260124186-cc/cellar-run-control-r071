package search

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

// SortOrder selects the deterministic ordering applied to a query.
type SortOrder string

const (
	SortLabel   SortOrder = "label"
	SortUpdated SortOrder = "updated"
)

const (
	DefaultLimit = 25
	MaxLimit     = 100
)

// Query is a single page request. The cursor must originate from a previous
// page that used the same filter, ordering, and direction; otherwise it is
// rejected so that pages cannot silently interleave two different result
// orders.
type Query struct {
	Filter     Filter
	Sort       SortOrder
	Descending bool
	Limit      int
	Cursor     string
}

// Position is the stable keyset position embedded in a cursor.
type Position struct {
	Label     string
	UpdatedAt time.Time
	ID        string
	Kind      Kind
}

type cursorPayload struct {
	Version    int       `json:"v"`
	Sort       SortOrder `json:"s"`
	Descending bool      `json:"d"`
	Label      string    `json:"l,omitempty"`
	UpdatedAt  time.Time `json:"u,omitempty"`
	ID         string    `json:"i"`
	Kind       Kind      `json:"k"`
}

// EncodeCursor renders an opaque cursor for the given position. The cursor is
// bound to the query shape so it cannot be replayed against an incompatible
// request.
func EncodeCursor(position Position, query Query) (string, error) {
	payload := cursorPayload{
		Version:    1,
		Sort:       query.Sort,
		Descending: query.Descending,
		ID:         position.ID,
		Kind:       position.Kind,
	}
	if query.Sort == SortUpdated {
		payload.UpdatedAt = position.UpdatedAt.UTC()
	} else {
		payload.Label = position.Label
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// DecodeCursor validates an opaque cursor and returns the position it names.
func DecodeCursor(value string, query Query) (Position, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Position{}, ErrInvalidCursor
	}
	var payload cursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Position{}, ErrInvalidCursor
	}
	if payload.Version != 1 ||
		payload.Sort != query.Sort ||
		payload.Descending != query.Descending ||
		payload.ID == "" || payload.Kind == "" {
		return Position{}, ErrInvalidCursor
	}
	return Position{
		Label:     payload.Label,
		UpdatedAt: payload.UpdatedAt,
		ID:        payload.ID,
		Kind:      payload.Kind,
	}, nil
}

// ErrInvalidCursor indicates a cursor that is malformed or bound to a
// different query shape.
var ErrInvalidCursor = errors.New("cursor is invalid or does not match this query")

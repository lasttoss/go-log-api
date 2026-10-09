package ingest

import (
	"fmt"
	"strings"
	"time"

	"github.com/lasttoss/go-log-api/internal/store"
)

// EventRequest is the wire shape of one event. Only the fields a game server is expected to know
// are required; value defaults to zero so a plain gameplay event needs no arithmetic.
type EventRequest struct {
	GameID     string  `json:"game_id"`
	UserID     string  `json:"user_id"`
	Type       string  `json:"type"`
	Value      float64 `json:"value"`
	OccurredAt string  `json:"occurred_at"`
}

// ValidatedEvent is an EventRequest that passed validation and an Event ready to be stored.
type ValidatedEvent struct {
	Event store.Event
}

const (
	maxFieldLen = 128
	futureSkew  = 5 * time.Minute
	maxEventAge = 30 * 24 * time.Hour
)

// Validate turns a request into a storable event, or explains what is wrong with it.
//
// Anything a game client can get wrong is rejected here, at the edge, so the write path and the
// rollup never have to deal with half-filled rows.
func Validate(req EventRequest, now time.Time) (ValidatedEvent, error) {
	gameID := strings.TrimSpace(req.GameID)
	userID := strings.TrimSpace(req.UserID)
	eventType := strings.TrimSpace(req.Type)

	switch {
	case gameID == "":
		return ValidatedEvent{}, fmt.Errorf("game_id is required")
	case userID == "":
		return ValidatedEvent{}, fmt.Errorf("user_id is required")
	case eventType == "":
		return ValidatedEvent{}, fmt.Errorf("type is required")
	case len(gameID) > maxFieldLen || len(userID) > maxFieldLen || len(eventType) > maxFieldLen:
		return ValidatedEvent{}, fmt.Errorf("game_id, user_id and type must be at most %d characters", maxFieldLen)
	}

	occurredAt := now
	if req.OccurredAt != "" {
		parsed, err := time.Parse(time.RFC3339, req.OccurredAt)
		if err != nil {
			return ValidatedEvent{}, fmt.Errorf("occurred_at must be RFC3339 (got %q)", req.OccurredAt)
		}
		occurredAt = parsed
	}
	if occurredAt.After(now.Add(futureSkew)) {
		return ValidatedEvent{}, fmt.Errorf("occurred_at is in the future")
	}
	if occurredAt.Before(now.Add(-maxEventAge)) {
		return ValidatedEvent{}, fmt.Errorf("occurred_at is older than %s", maxEventAge)
	}

	return ValidatedEvent{Event: store.Event{
		GameID:     gameID,
		UserID:     userID,
		Type:       eventType,
		Value:      req.Value,
		OccurredAt: occurredAt.UTC(),
	}}, nil
}

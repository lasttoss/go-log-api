package ingest

import (
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		req     EventRequest
		wantErr string
	}{
		{
			name: "minimal event defaults occurred_at to now",
			req:  EventRequest{GameID: "cozy-garden", UserID: "u-1", Type: "login"},
		},
		{
			name:    "game_id is required",
			req:     EventRequest{UserID: "u-1", Type: "login"},
			wantErr: "game_id is required",
		},
		{
			name:    "user_id is required",
			req:     EventRequest{GameID: "cozy-garden", Type: "login"},
			wantErr: "user_id is required",
		},
		{
			name:    "type is required",
			req:     EventRequest{GameID: "cozy-garden", UserID: "u-1"},
			wantErr: "type is required",
		},
		{
			name:    "whitespace is not an id",
			req:     EventRequest{GameID: "  ", UserID: "u-1", Type: "login"},
			wantErr: "game_id is required",
		},
		{
			name:    "occurred_at must parse",
			req:     EventRequest{GameID: "g", UserID: "u-1", Type: "login", OccurredAt: "yesterday"},
			wantErr: "occurred_at must be RFC3339 (got \"yesterday\")",
		},
		{
			name:    "occurred_at cannot be in the future",
			req:     EventRequest{GameID: "g", UserID: "u-1", Type: "login", OccurredAt: now.Add(30 * time.Minute).Format(time.RFC3339)},
			wantErr: "occurred_at is in the future",
		},
		{
			name:    "occurred_at cannot be ancient",
			req:     EventRequest{GameID: "g", UserID: "u-1", Type: "login", OccurredAt: now.Add(-60 * 24 * time.Hour).Format(time.RFC3339)},
			wantErr: "occurred_at is older than 720h0m0s",
		},
		{
			name: "a value is carried through",
			req:  EventRequest{GameID: "g", UserID: "u-1", Type: "purchase", Value: 4.99},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Validate(tc.req, now)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("Validate() = nil error, want %q", tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("Validate() error = %q, want %q", err.Error(), tc.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("Validate() unexpected error: %v", err)
			}
			if !got.Event.OccurredAt.Equal(now) && tc.req.OccurredAt == "" {
				t.Fatalf("OccurredAt = %v, want %v", got.Event.OccurredAt, now)
			}
			if got.Event.Value != tc.req.Value {
				t.Fatalf("Value = %v, want %v", got.Event.Value, tc.req.Value)
			}
		})
	}
}

func TestValidate_TrimsAndKeepsUTC(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	got, err := Validate(EventRequest{
		GameID: "  cozy-garden ", UserID: " u-1", Type: "login ",
		OccurredAt: "2026-10-09T10:00:00+07:00",
	}, now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Event.GameID != "cozy-garden" || got.Event.UserID != "u-1" || got.Event.Type != "login" {
		t.Fatalf("fields were not trimmed: %+v", got.Event)
	}
	if !got.Event.OccurredAt.Equal(time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC)) {
		t.Fatalf("OccurredAt = %v, want 2026-10-09T03:00:00Z", got.Event.OccurredAt)
	}
}

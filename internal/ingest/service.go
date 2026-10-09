package ingest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/lasttoss/go-log-api/internal/metrics"
	"github.com/lasttoss/go-log-api/internal/store"
)

// Reader is the read side of the rollup table.
type Reader interface {
	DailyStats(ctx context.Context, gameID string, from, to time.Time) ([]store.DailyStat, error)
}

// Service exposes the HTTP API.
type Service struct {
	Reader   Reader
	Batcher  *Batcher
	Counters *metrics.Counters
	MaxBatch int
	Logger   *slog.Logger
	Now      func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// HandleIngest accepts {"events":[...]}.
func (s *Service) HandleIngest(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Events []EventRequest `json:"events"`
	}

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := decoder.Decode(&payload); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body: " + err.Error()})
		return
	}

	if len(payload.Events) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no events in the request"})
		return
	}
	if len(payload.Events) > s.MaxBatch {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{
			"error": "batch too large", "max_batch": s.MaxBatch, "received": len(payload.Events),
		})
		return
	}

	now := s.now()
	accepted, rejected := 0, 0
	problems := []map[string]string{}

	for i, req := range payload.Events {
		event, err := Validate(req, now)
		if err != nil {
			rejected++
			s.Counters.EventsRejected.Add(1)
			if len(problems) < 10 {
				problems = append(problems, map[string]string{"index": strconv.Itoa(i), "error": err.Error()})
			}
			continue
		}
		if !s.Batcher.Submit(event.Event) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{
				"error": "ingest buffer is full, retry shortly", "queued": accepted,
			})
			return
		}
		accepted++
		s.Counters.EventsAccepted.Add(1)
	}

	status := http.StatusAccepted
	if accepted == 0 {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]any{
		"accepted": accepted,
		"rejected": rejected,
		"problems": problems,
	})
}

// HandleStats serves the daily rollups for one game.
func (s *Service) HandleStats(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	gameID := query.Get("game_id")
	if gameID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "game_id is required"})
		return
	}

	now := s.now()
	from := now.AddDate(0, 0, -7)
	if v := query.Get("from"); v != "" {
		parsed, err := time.Parse("2006-01-02", v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "from must be YYYY-MM-DD"})
			return
		}
		from = parsed
	}
	to := now
	if v := query.Get("to"); v != "" {
		parsed, err := time.Parse("2006-01-02", v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to must be YYYY-MM-DD"})
			return
		}
		to = parsed
	}
	if to.Before(from) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "to must not be before from"})
		return
	}

	stats, err := s.Reader.DailyStats(r.Context(), gameID, from, to)
	if err != nil {
		s.logger().Error("stats query failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"game_id": gameID,
		"from":    from.Format("2006-01-02"),
		"to":      to.Format("2006-01-02"),
		"daily":   stats,
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

package store

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lasttoss/go-log-api/internal/metrics"
)

// Event is one gameplay event as it is stored.
type Event struct {
	GameID     string
	UserID     string
	Type       string
	Value      float64
	OccurredAt time.Time
}

// Store owns the connection pool and every SQL statement in the service.
type Store struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, url string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Close() { s.pool.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate creates the schema. It is idempotent and cheap, so it runs at boot: a demo that needs
// a manual migration step is a demo nobody runs twice.
func (s *Store) Migrate(ctx context.Context) error {
	const schema = `
CREATE TABLE IF NOT EXISTS events (
    id          bigserial PRIMARY KEY,
    game_id     text          NOT NULL,
    user_id     text          NOT NULL,
    event_type  text          NOT NULL,
    value       numeric(18,4) NOT NULL DEFAULT 0,
    occurred_at timestamptz   NOT NULL,
    received_at timestamptz   NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS events_occurred_at_idx ON events (occurred_at);
CREATE INDEX IF NOT EXISTS events_game_occurred_at_idx ON events (game_id, occurred_at);

CREATE TABLE IF NOT EXISTS metrics_daily (
    game_id    text          NOT NULL,
    day        date          NOT NULL,
    events     bigint        NOT NULL DEFAULT 0,
    dau        bigint        NOT NULL DEFAULT 0,
    value_sum  numeric(18,4) NOT NULL DEFAULT 0,
    updated_at timestamptz   NOT NULL DEFAULT now(),
    PRIMARY KEY (game_id, day)
);`
	_, err := s.pool.Exec(ctx, schema)
	return err
}

// InsertEvents writes a whole batch with one multi-row INSERT.
func (s *Store) InsertEvents(ctx context.Context, events []Event) error {
	if len(events) == 0 {
		return nil
	}

	sql := "INSERT INTO events (game_id, user_id, event_type, value, occurred_at) VALUES "
	values := make([]any, 0, len(events)*5)
	for i, e := range events {
		if i > 0 {
			sql += ","
		}
		arg := i*5 + 1
		sql += fmt.Sprintf("($%d,$%d,$%d,$%d,$%d)", arg, arg+1, arg+2, arg+3, arg+4)
		values = append(values, e.GameID, e.UserID, e.Type, e.Value, e.OccurredAt)
	}

	_, err := s.pool.Exec(ctx, sql, values...)
	return err
}

// DailyStat is one rolled-up row.
type DailyStat struct {
	GameID   string  `json:"game_id"`
	Day      string  `json:"day"`
	Events   int64   `json:"events"`
	DAU      int64   `json:"dau"`
	ValueSum float64 `json:"value_sum"`
}

// Rollup recomputes the daily aggregates for everything that arrived in the window.
//
// The work happens in the database, in one statement: counting distinct players per game per day
// in the application would mean streaming every event back over the wire, and the read side only
// ever needs the aggregate.
func (s *Store) Rollup(ctx context.Context, since time.Time) (int64, error) {
	const q = `
INSERT INTO metrics_daily (game_id, day, events, dau, value_sum, updated_at)
SELECT
    game_id,
    date_trunc('day', occurred_at)::date AS day,
    count(*)                             AS events,
    count(DISTINCT user_id)              AS dau,
    coalesce(sum(value), 0)              AS value_sum,
    now()
FROM events
WHERE occurred_at >= $1
GROUP BY game_id, day
ON CONFLICT (game_id, day) DO UPDATE SET
    events     = EXCLUDED.events,
    dau        = EXCLUDED.dau,
    value_sum  = EXCLUDED.value_sum,
    updated_at = EXCLUDED.updated_at`
	tag, err := s.pool.Exec(ctx, q, since)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// DailyStats reads the rollups for one game inside a date range.
func (s *Store) DailyStats(ctx context.Context, gameID string, from, to time.Time) ([]DailyStat, error) {
	const q = `
SELECT game_id, to_char(day, 'YYYY-MM-DD'), events, dau, value_sum::float8
FROM metrics_daily
WHERE game_id = $1 AND day >= $2::date AND day <= $3::date
ORDER BY day`
	rows, err := s.pool.Query(ctx, q, gameID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DailyStat{}
	for rows.Next() {
		var st DailyStat
		if err := rows.Scan(&st.GameID, &st.Day, &st.Events, &st.DAU, &st.ValueSum); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// Roller recomputes the aggregates on an interval.
type Roller struct {
	store    *Store
	logger   *slog.Logger
	interval time.Duration
	counters *metrics.Counters
	window   time.Duration
}

func NewRoller(s *Store, logger *slog.Logger, interval time.Duration, counters *metrics.Counters) *Roller {
	return &Roller{
		store:    s,
		logger:   logger,
		interval: interval,
		counters: counters,
		window:   7 * 24 * time.Hour,
	}
}

func (r *Roller) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rows, err := r.store.Rollup(ctx, time.Now().Add(-r.window))
			if err != nil {
				r.counters.IncRollupError()
				r.logger.Error("rollup failed", "err", err)
				continue
			}
			r.counters.IncRollup()
			r.logger.Debug("rollup done", "rows", rows)
		}
	}
}

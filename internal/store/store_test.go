package store_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lasttoss/go-log-api/internal/metrics"
	"github.com/lasttoss/go-log-api/internal/store"
)

// What is under test here is the SQL, so these tests need a real PostgreSQL: a fake would only
// prove that the rows it was handed come back out. They are skipped unless TEST_DATABASE_URL is
// set - which is how CI runs them against a postgres service container, and how they run locally:
//
//	make infra
//	TEST_DATABASE_URL='postgres://postgres:localdb@localhost:5432/gamelogs?sslmode=disable' go test ./internal/store/
//
// The tests own the database they are pointed at: the fixture drops the two tables these tests use,
// so Migrate has something to create. Do not point it at anything worth keeping.
//
// The dates in these tests are fixed and in the past, so that "yesterday" means the same thing on
// every run; they assume the database runs in UTC, which the compose file and the CI container do.

const (
	gameA = "cozy-garden"
	gameB = "star-salvage"
)

type fixture struct {
	*store.Store
	raw *pgxpool.Pool
}

func open(t *testing.T) fixture {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set: these tests need a real PostgreSQL (see the comment above)")
	}

	ctx := context.Background()
	s, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)

	raw, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("raw pool: %v", err)
	}
	t.Cleanup(raw.Close)

	if _, err := raw.Exec(ctx, "DROP TABLE IF EXISTS events; DROP TABLE IF EXISTS metrics_daily"); err != nil {
		t.Fatalf("dropping the tables the tests use: %v", err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return fixture{Store: s, raw: raw}
}

func (f fixture) count(t *testing.T, table string) int64 {
	t.Helper()
	var n int64
	if err := f.raw.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("counting %s: %v", table, err)
	}
	return n
}

// occurred builds an event at a fixed UTC instant, so the day it lands in never changes.
func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func occurred(day time.Time, hour int) time.Time {
	return day.Add(time.Duration(hour) * time.Hour)
}

var (
	day1 = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	day2 = time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC)
	day3 = time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC)
)

func eventAt(game, user, kind string, value float64, at time.Time) store.Event {
	return store.Event{GameID: game, UserID: user, Type: kind, Value: value, OccurredAt: at}
}

func TestOpen_RejectsAURLItCannotParse(t *testing.T) {
	if _, err := store.Open(context.Background(), "not-a-url"); err == nil {
		t.Fatal("Open accepted a URL that is not one")
	} else if !strings.Contains(err.Error(), "parse DATABASE_URL") {
		t.Errorf("error = %v, want it to mention DATABASE_URL", err)
	}
}

// "the database is not up yet" is the most likely way this service fails on a fresh machine, and it
// has to come back as an error rather than as a pool the caller will wait on forever.
func TestOpen_ReportsADatabaseThatIsNotListening(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := store.Open(ctx, "postgres://postgres:localdb@127.0.0.1:1/gamelogs?sslmode=disable"); err == nil {
		t.Fatal("Open reported success against a port with nothing on it")
	}
}

func TestMigrate_IsIdempotentAndLeavesTheIndexesBehind(t *testing.T) {
	f := open(t)

	if err := f.Migrate(context.Background()); err != nil {
		t.Fatalf("the second Migrate failed: %v", err)
	}

	for _, table := range []string{"events", "metrics_daily"} {
		var exists *string
		if err := f.raw.QueryRow(context.Background(), "SELECT to_regclass('public."+table+"')::text").Scan(&exists); err != nil {
			t.Fatalf("to_regclass(%s): %v", table, err)
		}
		if exists == nil {
			t.Errorf("table %s was not created", table)
		}
	}

	var indexes int
	if err := f.raw.QueryRow(context.Background(),
		"SELECT count(*) FROM pg_indexes WHERE tablename = 'events'").Scan(&indexes); err != nil {
		t.Fatalf("counting indexes: %v", err)
	}
	if indexes != 3 { // the primary key plus the two the queries need
		t.Errorf("events has %d indexes, want 3", indexes)
	}
}

func TestInsertEvents_WritesTheWholeBatch(t *testing.T) {
	f := open(t)

	batch := []store.Event{
		eventAt(gameA, "u-1", "login", 0, occurred(day1, 10)),
		eventAt(gameA, "u-1", "purchase", 4.5, occurred(day1, 11)),
		eventAt(gameB, "u-9", "login", 0, occurred(day2, 9)),
	}
	if err := f.InsertEvents(context.Background(), batch); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}
	if got := f.count(t, "events"); got != 3 {
		t.Fatalf("events = %d, want 3", got)
	}

	var (
		value    float64
		gameID   string
		storedAt time.Time
	)
	err := f.raw.QueryRow(context.Background(),
		`SELECT value::float8, game_id, occurred_at FROM events WHERE event_type = 'purchase'`).
		Scan(&value, &gameID, &storedAt)
	if err != nil {
		t.Fatalf("reading the purchase row back: %v", err)
	}
	if value != 4.5 || gameID != gameA || !storedAt.Equal(occurred(day1, 11)) {
		t.Errorf("stored (%v, %s, %s), want (4.5, %s, %s)", value, gameID, storedAt, gameA, occurred(day1, 11))
	}
}

func TestInsertEvents_WithoutEventsWritesNothing(t *testing.T) {
	f := open(t)

	if err := f.InsertEvents(context.Background(), nil); err != nil {
		t.Fatalf("InsertEvents(nil): %v", err)
	}
	if err := f.InsertEvents(context.Background(), []store.Event{}); err != nil {
		t.Fatalf("InsertEvents(empty): %v", err)
	}
	if got := f.count(t, "events"); got != 0 {
		t.Errorf("events = %d, want 0", got)
	}
}

func TestRollup_CountsEventsDistinctPlayersAndValue(t *testing.T) {
	f := open(t)
	ctx := context.Background()

	batch := []store.Event{
		eventAt(gameA, "u-1", "login", 0, occurred(day1, 10)),
		eventAt(gameA, "u-1", "purchase", 1.5, occurred(day1, 12)),
		eventAt(gameA, "u-2", "login", 0, occurred(day1, 13)),
		eventAt(gameA, "u-1", "login", 0, occurred(day2, 10)),
		eventAt(gameB, "u-1", "login", 0, occurred(day1, 10)),
	}
	if err := f.InsertEvents(ctx, batch); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}

	rows, err := f.Rollup(ctx, day1)
	if err != nil {
		t.Fatalf("Rollup: %v", err)
	}
	if rows != 3 { // gameA/day1, gameA/day2, gameB/day1
		t.Fatalf("Rollup wrote %d rows, want 3", rows)
	}

	stats, err := f.DailyStats(ctx, gameA, day1, day3)
	if err != nil {
		t.Fatalf("DailyStats: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("got %d days for %s, want 2: %+v", len(stats), gameA, stats)
	}
	if stats[0].Day != "2026-01-05" || stats[0].Events != 3 || stats[0].DAU != 2 || stats[0].ValueSum != 1.5 {
		t.Errorf("day one = %+v, want 3 events, 2 players, value 1.5", stats[0])
	}
	if stats[1].Day != "2026-01-06" || stats[1].Events != 1 || stats[1].DAU != 1 {
		t.Errorf("day two = %+v, want 1 event from 1 player", stats[1])
	}

	other, err := f.DailyStats(ctx, gameB, day1, day3)
	if err != nil {
		t.Fatalf("DailyStats for %s: %v", gameB, err)
	}
	if len(other) != 1 || other[0].GameID != gameB {
		t.Errorf("the other game = %+v", other)
	}
}

// The roller runs every few seconds forever, so a rollup that appended would grow the table without
// bound. It upserts, and a second run over the same events has to leave the same numbers behind.
func TestRollup_ReplacesInsteadOfAccumulating(t *testing.T) {
	f := open(t)
	ctx := context.Background()

	if err := f.InsertEvents(ctx, []store.Event{eventAt(gameA, "u-1", "login", 2, occurred(day1, 10))}); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}
	for range 3 {
		if _, err := f.Rollup(ctx, day1); err != nil {
			t.Fatalf("Rollup: %v", err)
		}
	}
	if got := f.count(t, "metrics_daily"); got != 1 {
		t.Fatalf("metrics_daily holds %d rows after three rollups, want 1", got)
	}

	// A late event changes the numbers of the day it belongs to, which is why the upsert updates.
	if err := f.InsertEvents(ctx, []store.Event{eventAt(gameA, "u-2", "login", 0, occurred(day1, 14))}); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}
	if _, err := f.Rollup(ctx, day1); err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	stats, err := f.DailyStats(ctx, gameA, day1, day3)
	if err != nil {
		t.Fatalf("DailyStats: %v", err)
	}
	if len(stats) != 1 || stats[0].Events != 2 || stats[0].DAU != 2 || stats[0].ValueSum != 2 {
		t.Errorf("after the late event = %+v, want 2 events, 2 players, value 2", stats)
	}
}

func TestRollup_IgnoresEventsBeforeTheWindow(t *testing.T) {
	f := open(t)
	ctx := context.Background()

	if err := f.InsertEvents(ctx, []store.Event{eventAt(gameA, "u-1", "login", 0, occurred(day1, 10))}); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}
	rows, err := f.Rollup(ctx, day2)
	if err != nil {
		t.Fatalf("Rollup: %v", err)
	}
	if rows != 0 {
		t.Errorf("Rollup wrote %d rows for a window the event is outside of, want 0", rows)
	}
	if stats, err := f.DailyStats(ctx, gameA, day1, day3); err != nil {
		t.Fatalf("DailyStats: %v", err)
	} else if len(stats) != 0 {
		t.Errorf("daily stats = %+v, want none", stats)
	}
}

func TestDailyStats_ReturnsTheRangeInOrder(t *testing.T) {
	f := open(t)
	ctx := context.Background()

	batch := []store.Event{
		eventAt(gameA, "u-1", "login", 0, occurred(day1, 10)),
		eventAt(gameA, "u-1", "login", 0, occurred(day2, 10)),
		eventAt(gameA, "u-1", "login", 0, occurred(day3, 10)),
	}
	if err := f.InsertEvents(ctx, batch); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}
	if _, err := f.Rollup(ctx, day1); err != nil {
		t.Fatalf("Rollup: %v", err)
	}

	stats, err := f.DailyStats(ctx, gameA, day1, day2)
	if err != nil {
		t.Fatalf("DailyStats: %v", err)
	}
	if len(stats) != 2 || stats[0].Day != "2026-01-05" || stats[1].Day != "2026-01-06" {
		t.Errorf("range = %+v, want the first two days in order", stats)
	}

	// An empty range is an empty list, not an error: the caller is a chart waiting to draw a line.
	if stats, err := f.DailyStats(ctx, "no-such-game", day1, day3); err != nil {
		t.Fatalf("DailyStats for a game with no events: %v", err)
	} else if len(stats) != 0 {
		t.Errorf("stats for a game with no events = %+v", stats)
	}
}

func TestPing_ReportsAHealthyConnection(t *testing.T) {
	f := open(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestClose_EndsTheConnection(t *testing.T) {
	f := open(t)
	f.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.Ping(ctx); err == nil {
		t.Error("Ping succeeded after Close")
	}
}

// The roller is a goroutine nobody looks at, so the test watches the counter and the table it is
// supposed to fill, and checks that cancelling the context really stops it.
func TestRoller_RollsUpOnItsIntervalUntilCancelled(t *testing.T) {
	f := open(t)
	ctx := context.Background()

	// The roller's window is fixed at seven days, so this event has to be recent - the fixed dates
	// the other tests use would fall outside it and prove nothing.
	if err := f.InsertEvents(ctx, []store.Event{
		eventAt(gameA, "u-1", "login", 3, time.Now().UTC().Add(-time.Hour)),
	}); err != nil {
		t.Fatalf("InsertEvents: %v", err)
	}

	counters := metrics.New()
	roller := store.NewRoller(f.Store, quietLogger(), 20*time.Millisecond, counters)

	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		roller.Run(runCtx)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for counters.Rollups.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if counters.Rollups.Load() == 0 {
		stop()
		t.Fatal("the roller ran for five seconds without rolling anything up")
	}
	if counters.RollupErrors.Load() != 0 {
		t.Errorf("RollupErrors = %d, want 0", counters.RollupErrors.Load())
	}

	today := time.Now().UTC()
	stats, err := f.DailyStats(ctx, gameA, today.AddDate(0, 0, -2), today.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("DailyStats: %v", err)
	}
	if len(stats) != 1 || stats[0].Events != 1 || stats[0].DAU != 1 || stats[0].ValueSum != 3 {
		t.Errorf("what the roller wrote = %+v, want the one event with value 3", stats)
	}

	stop()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Error("Run did not return after its context was cancelled")
	}
}

package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lasttoss/go-log-api/internal/ingest"
	"github.com/lasttoss/go-log-api/internal/metrics"
	"github.com/lasttoss/go-log-api/internal/store"
)

// The HTTP layer is where a game client finds out what it got wrong, so what is worth testing here
// is the contract: which status, which body, and what the counters say afterwards. The write path
// itself is tested in batcher_test.go and the SQL in store_test.go.

type fakeReader struct {
	stats []store.DailyStat
	err   error

	calls  int
	gameID string
	from   time.Time
	to     time.Time
}

func (f *fakeReader) DailyStats(_ context.Context, gameID string, from, to time.Time) ([]store.DailyStat, error) {
	f.calls++
	f.gameID, f.from, f.to = gameID, from, to
	return f.stats, f.err
}

type serviceFixture struct {
	service *ingest.Service
	reader  *fakeReader
	now     time.Time
}

// newService builds a Service the way main.go does, with the clock pinned so the assertions about
// dates mean something.
func newService(t *testing.T, maxBatch int) serviceFixture {
	t.Helper()
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	reader := &fakeReader{}
	return serviceFixture{
		service: &ingest.Service{
			Reader:   reader,
			Batcher:  ingest.NewBatcher(&recordingWriter{}, quietLogger(), maxBatch, time.Second, metrics.New()),
			Counters: metrics.New(),
			MaxBatch: maxBatch,
			Now:      func() time.Time { return now },
		},
		reader: reader,
		now:    now,
	}
}

func postEvents(s *ingest.Service, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/v1/events", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	s.HandleIngest(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body is not JSON (%v): %s", err, rec.Body.String())
	}
	return out
}

func TestHandleIngest_AcceptsAndCounts(t *testing.T) {
	f := newService(t, 100)
	rec := postEvents(f.service, `{"events":[
		{"game_id":"cozy-garden","user_id":"u-1","type":"login"},
		{"game_id":"cozy-garden","user_id":"u-2","type":"purchase","value":4.99}]}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["accepted"] != 2.0 || body["rejected"] != 0.0 {
		t.Errorf("accepted/rejected = %v/%v, want 2/0", body["accepted"], body["rejected"])
	}
	if got := f.service.Counters.EventsAccepted.Load(); got != 2 {
		t.Errorf("EventsAccepted = %d, want 2", got)
	}
	if got := f.service.Counters.EventsRejected.Load(); got != 0 {
		t.Errorf("EventsRejected = %d, want 0", got)
	}
}

func TestHandleIngest_RejectsInvalidJSON(t *testing.T) {
	f := newService(t, 100)
	rec := postEvents(f.service, `{"events":[`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if msg, _ := decode(t, rec)["error"].(string); !strings.HasPrefix(msg, "invalid JSON body") {
		t.Errorf("error = %q, want it to start with %q", msg, "invalid JSON body")
	}
}

func TestHandleIngest_RejectsAnEmptyBatch(t *testing.T) {
	f := newService(t, 100)
	rec := postEvents(f.service, `{"events":[]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if msg, _ := decode(t, rec)["error"].(string); msg != "no events in the request" {
		t.Errorf("error = %q", msg)
	}
	if got := f.service.Counters.EventsAccepted.Load(); got != 0 {
		t.Errorf("EventsAccepted = %d, want 0", got)
	}
}

// Refusing an oversized batch outright beats accepting half of it: the client can split the request
// and know exactly what happened to each event.
func TestHandleIngest_RefusesABatchAboveTheLimit(t *testing.T) {
	f := newService(t, 2)
	rec := postEvents(f.service, `{"events":[
		{"game_id":"g","user_id":"u-1","type":"login"},
		{"game_id":"g","user_id":"u-2","type":"login"},
		{"game_id":"g","user_id":"u-3","type":"login"}]}`)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["max_batch"] != 2.0 || body["received"] != 3.0 {
		t.Errorf("max_batch/received = %v/%v, want 2/3", body["max_batch"], body["received"])
	}
	if got := f.service.Counters.EventsAccepted.Load(); got != 0 {
		t.Errorf("EventsAccepted = %d, want 0", got)
	}
}

// A batch where every event is wrong is a client-side bug, and 400 says so; a batch with some good
// events in it is answered with 202 and a list of what was skipped.
func TestHandleIngest_ExplainsWhichEventsWereRejected(t *testing.T) {
	f := newService(t, 100)
	rec := postEvents(f.service, `{"events":[
		{"user_id":"u-1","type":"login"},
		{"game_id":"cozy-garden","user_id":"u-2","type":"login"},
		{"game_id":"cozy-garden","user_id":"u-3","type":"login","occurred_at":"not-a-date"}]}`)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["accepted"] != 1.0 || body["rejected"] != 2.0 {
		t.Errorf("accepted/rejected = %v/%v, want 1/2", body["accepted"], body["rejected"])
	}

	problems, _ := body["problems"].([]any)
	if len(problems) != 2 {
		t.Fatalf("problems = %v, want 2", problems)
	}
	first, _ := problems[0].(map[string]any)
	if first["index"] != "0" || first["error"] != "game_id is required" {
		t.Errorf("problems[0] = %v", first)
	}
	second, _ := problems[1].(map[string]any)
	if second["index"] != "2" || !strings.Contains(second["error"].(string), "RFC3339") {
		t.Errorf("problems[1] = %v", second)
	}
	if got := f.service.Counters.EventsRejected.Load(); got != 2 {
		t.Errorf("EventsRejected = %d, want 2", got)
	}
}

func TestHandleIngest_SaysBadRequestWhenNothingCouldBeAccepted(t *testing.T) {
	f := newService(t, 100)
	rec := postEvents(f.service, `{"events":[{"user_id":"u-1","type":"login"}]}`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if accepted := decode(t, rec)["accepted"]; accepted != 0.0 {
		t.Errorf("accepted = %v, want 0", accepted)
	}
}

// The answer to "everything is wrong" must not be proportional to the request: one line per bad
// event means a megabyte of problems in the response to a broken client.
func TestHandleIngest_CapsTheProblemList(t *testing.T) {
	f := newService(t, 100)
	events := make([]string, 12)
	for i := range events {
		events[i] = `{"user_id":"u-1","type":"login"}`
	}
	rec := postEvents(f.service, `{"events":[`+strings.Join(events, ",")+`]}`)

	body := decode(t, rec)
	if body["rejected"] != 12.0 {
		t.Errorf("rejected = %v, want 12", body["rejected"])
	}
	if problems, _ := body["problems"].([]any); len(problems) != 10 {
		t.Errorf("problems length = %d, want 10", len(problems))
	}
}

// When the buffer is full the service says so and asks for a retry, rather than blocking the
// request or growing the queue until the process dies.
func TestHandleIngest_AsksForARetryWhenTheBufferIsFull(t *testing.T) {
	f := newService(t, 1) // NewBatcher sizes the queue at 4x the batch
	one := `{"events":[{"game_id":"g","user_id":"u-1","type":"login"}]}`

	var last *httptest.ResponseRecorder
	for range 10 {
		last = postEvents(f.service, one)
		if last.Code == http.StatusServiceUnavailable {
			break
		}
		if last.Code != http.StatusAccepted {
			t.Fatalf("unexpected status %d: %s", last.Code, last.Body.String())
		}
	}

	if last.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 once the queue is full", last.Code)
	}
	body := decode(t, last)
	if msg, _ := body["error"].(string); !strings.Contains(msg, "retry") {
		t.Errorf("error = %q", msg)
	}
	// "queued" counts what this request managed to queue, which on the refusal path is nothing -
	// the events it could not take are the caller's to resend, and the error says so.
	if got := body["queued"]; got != 0.0 {
		t.Errorf("queued = %v, want 0 for the request that was refused", got)
	}
}

func TestHandleStats_RequiresAGameID(t *testing.T) {
	f := newService(t, 100)
	rec := httptest.NewRecorder()
	f.service.HandleStats(rec, httptest.NewRequest("GET", "/v1/stats", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if msg, _ := decode(t, rec)["error"].(string); msg != "game_id is required" {
		t.Errorf("error = %q", msg)
	}
	if f.reader.calls != 0 {
		t.Errorf("the reader was called %d times, want 0", f.reader.calls)
	}
}

func TestHandleStats_RejectsUnparseableDates(t *testing.T) {
	bad := map[string]string{
		"/v1/stats?game_id=g&from=09-10-2026": "from must be YYYY-MM-DD",
		"/v1/stats?game_id=g&to=09-10-2026":   "to must be YYYY-MM-DD",
	}
	for url, want := range bad {
		f := newService(t, 100)
		rec := httptest.NewRecorder()
		f.service.HandleStats(rec, httptest.NewRequest("GET", url, nil))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", url, rec.Code)
			continue
		}
		if msg, _ := decode(t, rec)["error"].(string); msg != want {
			t.Errorf("%s: error = %q, want %q", url, msg, want)
		}
	}
}

func TestHandleStats_RejectsAnInvertedRange(t *testing.T) {
	f := newService(t, 100)
	rec := httptest.NewRecorder()
	f.service.HandleStats(rec, httptest.NewRequest("GET", "/v1/stats?game_id=g&from=2026-10-09&to=2026-10-01", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if msg, _ := decode(t, rec)["error"].(string); msg != "to must not be before from" {
		t.Errorf("error = %q", msg)
	}
}

// No dates means the last week: the default has to be visible in the response, because the caller
// needs to know which window it just asked for.
func TestHandleStats_DefaultsToTheLastSevenDays(t *testing.T) {
	f := newService(t, 100)
	f.reader.stats = []store.DailyStat{{GameID: "cozy-garden", Day: "2026-10-08", Events: 12, DAU: 3, ValueSum: 4.5}}

	rec := httptest.NewRecorder()
	f.service.HandleStats(rec, httptest.NewRequest("GET", "/v1/stats?game_id=cozy-garden", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if body["from"] != "2026-10-02" || body["to"] != "2026-10-09" {
		t.Errorf("window = %v..%v, want 2026-10-02..2026-10-09", body["from"], body["to"])
	}
	if f.reader.gameID != "cozy-garden" || !f.reader.from.Equal(f.now.AddDate(0, 0, -7)) {
		t.Errorf("reader got game=%q from=%s", f.reader.gameID, f.reader.from)
	}
	daily, _ := body["daily"].([]any)
	if len(daily) != 1 {
		t.Fatalf("daily = %v, want the single row from the reader", body["daily"])
	}
	row, _ := daily[0].(map[string]any)
	if row["day"] != "2026-10-08" || row["events"] != 12.0 || row["dau"] != 3.0 || row["value_sum"] != 4.5 {
		t.Errorf("daily[0] = %v", row)
	}
}

func TestHandleStats_HonoursTheRangeItWasGiven(t *testing.T) {
	f := newService(t, 100)
	rec := httptest.NewRecorder()
	f.service.HandleStats(rec, httptest.NewRequest("GET", "/v1/stats?game_id=g&from=2026-09-01&to=2026-09-30", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	wantFrom := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	if !f.reader.from.Equal(wantFrom) || !f.reader.to.Equal(wantTo) {
		t.Errorf("reader got %s..%s, want %s..%s", f.reader.from, f.reader.to, wantFrom, wantTo)
	}
}

// A query failure is the service's problem, not the caller's, so it is a 500 with the detail in the
// log rather than in the response. The logger here is the default one, which is also the nil case.
func TestHandleStats_ReportsAQueryFailureAsATwentySomethingNot(t *testing.T) {
	f := newService(t, 100)
	f.reader.err = errors.New("connection refused")

	rec := httptest.NewRecorder()
	f.service.HandleStats(rec, httptest.NewRequest("GET", "/v1/stats?game_id=g", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := decode(t, rec)
	if body["error"] != "query failed" || strings.Contains(rec.Body.String(), "connection refused") {
		t.Errorf("the response leaked or changed the error: %s", rec.Body.String())
	}
}

package metrics_test

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lasttoss/go-log-api/internal/metrics"
)

// The exposition endpoint is the only place an operator sees what the service counted, so these
// tests read it the way Prometheus would: make the counters say something, then look for it.

func TestHandler_ExposesEveryCounter(t *testing.T) {
	c := metrics.New()
	c.EventsAccepted.Add(3)
	c.EventsRejected.Add(2)
	c.RowsWritten.Add(300)
	c.Flushes.Add(4)
	c.FlushErrors.Add(1)
	c.IncRollup()
	c.IncRollup()
	c.IncRollupError()
	c.ObserveIngest(0.125)
	c.ObserveIngest(0.125)

	rec := httptest.NewRecorder()
	c.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain; version=0.0.4; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	body := rec.Body.String()
	for _, want := range []string{
		"gamelog_events_accepted_total 3",
		"gamelog_events_rejected_total 2",
		"gamelog_rows_written_total 300",
		"gamelog_flush_total 4",
		"gamelog_flush_errors_total 1",
		"gamelog_rollup_total 2",
		"gamelog_rollup_errors_total 1",
		"gamelog_ingest_write_seconds_sum 0.250000",
		"gamelog_ingest_write_seconds_count 2",
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("body is missing %q\n---\n%s", want, body)
		}
	}
}

// A counter without a HELP and a TYPE line is a counter a scraper will drop, which is a quiet way
// for this endpoint to stop working.
func TestHandler_EveryMetricIsDeclaredBeforeItIsReported(t *testing.T) {
	c := metrics.New()
	c.ObserveIngest(1)

	rec := httptest.NewRecorder()
	c.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	declared := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if name, ok := strings.CutPrefix(line, "# TYPE "); ok {
			declared[strings.Fields(name)[0]] = true
		}
	}
	for _, line := range strings.Split(body, "\n") {
		name, _, ok := strings.Cut(line, " ")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		// The summary's _sum and _count share the type line of the summary itself.
		base := strings.TrimSuffix(strings.TrimSuffix(name, "_sum"), "_count")
		if !declared[name] && !declared[base] {
			t.Errorf("metric %q is reported without a # TYPE line\n---\n%s", name, body)
		}
	}
}

func TestHandler_FreshCountersReportZero(t *testing.T) {
	rec := httptest.NewRecorder()
	metrics.New().Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))

	body := rec.Body.String()
	for _, want := range []string{
		"gamelog_events_accepted_total 0",
		"gamelog_ingest_write_seconds_sum 0.000000",
		"gamelog_ingest_write_seconds_count 0",
	} {
		if !strings.Contains(body, want+"\n") {
			t.Errorf("body is missing %q\n---\n%s", want, body)
		}
	}
}

// The counters are shared by the HTTP handler, the batcher and the roller, so they are read and
// written from several goroutines. `make race` runs this with the detector on.
func TestCounters_ConcurrentUseLosesNothing(t *testing.T) {
	c := metrics.New()
	const goroutines, each = 50, 20

	var wg sync.WaitGroup
	for range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				c.EventsAccepted.Add(1)
				c.ObserveIngest(0.001)
			}
		}()
	}
	wg.Wait()

	if got := c.EventsAccepted.Load(); got != goroutines*each {
		t.Errorf("events accepted = %d, want %d", got, goroutines*each)
	}
	rec := httptest.NewRecorder()
	c.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if want := "gamelog_ingest_write_seconds_count 1000\n"; !strings.Contains(rec.Body.String(), want) {
		t.Errorf("body is missing %q", want)
	}
}

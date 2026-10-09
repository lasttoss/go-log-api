package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
)

// Counters is a tiny Prometheus exposition endpoint. It is deliberately not a client library:
// the service needs a handful of counters, and handing over four lines of text beats adding a
// dependency that has to be kept up to date.
type Counters struct {
	EventsAccepted atomic.Int64
	EventsRejected atomic.Int64
	RowsWritten    atomic.Int64
	Flushes        atomic.Int64
	FlushErrors    atomic.Int64
	Rollups        atomic.Int64
	RollupErrors   atomic.Int64

	mu       sync.Mutex
	seconds  float64
	observed int64
}

func New() *Counters { return &Counters{} }

// ObserveIngest records how long a batch of events spent being written.
func (c *Counters) ObserveIngest(seconds float64) {
	c.mu.Lock()
	c.seconds += seconds
	c.observed++
	c.mu.Unlock()
}

func (c *Counters) IncRollup()      { c.Rollups.Add(1) }
func (c *Counters) IncRollupError() { c.RollupErrors.Add(1) }

func (c *Counters) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		c.mu.Lock()
		seconds, observed := c.seconds, c.observed
		c.mu.Unlock()

		write := func(format string, args ...any) { fmt.Fprintf(w, format+"\n", args...) }

		write("# HELP gamelog_events_accepted_total Events accepted for writing.")
		write("# TYPE gamelog_events_accepted_total counter")
		write("gamelog_events_accepted_total %d", c.EventsAccepted.Load())

		write("# HELP gamelog_events_rejected_total Events rejected by validation.")
		write("# TYPE gamelog_events_rejected_total counter")
		write("gamelog_events_rejected_total %d", c.EventsRejected.Load())

		write("# HELP gamelog_rows_written_total Event rows written to postgres.")
		write("# TYPE gamelog_rows_written_total counter")
		write("gamelog_rows_written_total %d", c.RowsWritten.Load())

		write("# HELP gamelog_flush_total Write batches attempted.")
		write("# TYPE gamelog_flush_total counter")
		write("gamelog_flush_total %d", c.Flushes.Load())
		write("# HELP gamelog_flush_errors_total Write batches that failed.")
		write("# TYPE gamelog_flush_errors_total counter")
		write("gamelog_flush_errors_total %d", c.FlushErrors.Load())

		write("# HELP gamelog_rollup_total Rollup runs.")
		write("# TYPE gamelog_rollup_total counter")
		write("gamelog_rollup_total %d", c.Rollups.Load())
		write("# HELP gamelog_rollup_errors_total Rollup runs that failed.")
		write("# TYPE gamelog_rollup_errors_total counter")
		write("gamelog_rollup_errors_total %d", c.RollupErrors.Load())

		write("# HELP gamelog_ingest_write_seconds Time spent writing event batches.")
		write("# TYPE gamelog_ingest_write_seconds summary")
		write("gamelog_ingest_write_seconds_sum %f", seconds)
		write("gamelog_ingest_write_seconds_count %d", observed)
	})
}

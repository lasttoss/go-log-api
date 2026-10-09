package ingest

import (
	"context"
	"log/slog"
	"time"

	"github.com/lasttoss/go-log-api/internal/metrics"
	"github.com/lasttoss/go-log-api/internal/store"
)

// Writer is the part of the store the batcher needs.
type Writer interface {
	InsertEvents(ctx context.Context, events []store.Event) error
}

// Batcher collects events and writes them in batches.
//
// One INSERT per event makes the database the bottleneck long before the game is; batching is the
// cheapest fix that keeps the HTTP path from waiting on disk. When the buffer is full the batcher
// refuses new work instead of growing without bound - a log pipeline that OOMs the service it is
// reporting on is worse than one that drops events and says so.
type Batcher struct {
	writer   Writer
	logger   *slog.Logger
	counters *metrics.Counters
	maxBatch int
	flush    time.Duration
	queue    chan store.Event
}

func NewBatcher(writer Writer, logger *slog.Logger, maxBatch int, flush time.Duration, counters *metrics.Counters) *Batcher {
	return &Batcher{
		writer:   writer,
		logger:   logger,
		counters: counters,
		maxBatch: maxBatch,
		flush:    flush,
		queue:    make(chan store.Event, maxBatch*4),
	}
}

// Submit queues one event and reports whether there was room for it.
func (b *Batcher) Submit(e store.Event) bool {
	select {
	case b.queue <- e:
		return true
	default:
		return false
	}
}

// Run drains the queue until the context is cancelled.
func (b *Batcher) Run(ctx context.Context) {
	ticker := time.NewTicker(b.flush)
	defer ticker.Stop()

	batch := make([]store.Event, 0, b.maxBatch)
	for {
		select {
		case <-ctx.Done():
			b.Flush(context.Background())
			return
		case e := <-b.queue:
			batch = append(batch, e)
			if len(batch) >= b.maxBatch {
				b.write(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			for len(batch) < b.maxBatch {
				select {
				case e := <-b.queue:
					batch = append(batch, e)
				default:
					goto drained
				}
			}
		drained:
			if len(batch) > 0 {
				b.write(batch)
				batch = batch[:0]
			}
		}
	}
}

// Flush writes whatever is buffered, including on shutdown.
func (b *Batcher) Flush(ctx context.Context) {
	batch := make([]store.Event, 0, b.maxBatch)
	for {
		select {
		case e := <-b.queue:
			batch = append(batch, e)
			if len(batch) >= b.maxBatch {
				b.write(batch)
				batch = batch[:0]
			}
		default:
			if len(batch) > 0 {
				b.write(batch)
			}
			return
		}
	}
}

func (b *Batcher) write(batch []store.Event) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := time.Now()
	if err := b.writer.InsertEvents(ctx, batch); err != nil {
		b.counters.FlushErrors.Add(1)
		b.logger.Error("batch write failed", "err", err, "events", len(batch))
		return
	}
	b.counters.Flushes.Add(1)
	b.counters.RowsWritten.Add(int64(len(batch)))
	b.counters.ObserveIngest(time.Since(started).Seconds())
}

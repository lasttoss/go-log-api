package ingest_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/lasttoss/go-log-api/internal/ingest"
	"github.com/lasttoss/go-log-api/internal/metrics"
	"github.com/lasttoss/go-log-api/internal/store"
)

type recordingWriter struct {
	mu      sync.Mutex
	batches [][]store.Event
}

func (w *recordingWriter) InsertEvents(ctx context.Context, events []store.Event) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.batches = append(w.batches, append([]store.Event(nil), events...))
	return nil
}

func (w *recordingWriter) written() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	total := 0
	for _, b := range w.batches {
		total += len(b)
	}
	return total
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func event(t string) store.Event {
	return store.Event{GameID: "cozy-garden", UserID: "u-1", Type: t, OccurredAt: time.Now()}
}

func TestBatcher_WritesWhenTheBatchIsFull(t *testing.T) {
	writer := &recordingWriter{}
	counters := metrics.New()
	b := ingest.NewBatcher(writer, quietLogger(), 3, time.Hour, counters)

	done := make(chan struct{})
	go func() { b.Run(context.Background()); close(done) }()

	for i := 0; i < 3; i++ {
		if !b.Submit(event("login")) {
			t.Fatalf("Submit refused event %d while the buffer was empty", i)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for writer.written() < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := writer.written(); got != 3 {
		t.Fatalf("wrote %d events, want 3", got)
	}
	if got := counters.RowsWritten.Load(); got != 3 {
		t.Fatalf("RowsWritten = %d, want 3", got)
	}
}

func TestBatcher_FlushDrainsTheBuffer(t *testing.T) {
	writer := &recordingWriter{}
	b := ingest.NewBatcher(writer, quietLogger(), 100, time.Hour, metrics.New())

	b.Submit(event("login"))
	b.Submit(event("purchase"))
	b.Flush(context.Background())

	if got := writer.written(); got != 2 {
		t.Fatalf("wrote %d events after Flush, want 2", got)
	}
}

func TestBatcher_RefusesWhenTheBufferIsFull(t *testing.T) {
	writer := &recordingWriter{}
	// maxBatch 1 buffers 4 events; Run() is never started, so nothing drains the queue.
	b := ingest.NewBatcher(writer, quietLogger(), 1, time.Hour, metrics.New())

	accepted := 0
	for i := 0; i < 50; i++ {
		if b.Submit(event("login")) {
			accepted++
		}
	}
	if accepted == 0 || accepted == 50 {
		t.Fatalf("accepted %d of 50 events: the buffer neither filled nor drained", accepted)
	}
}

// Command server is the event ingest + metrics rollup API.
//
//	POST /v1/events          batch of gameplay events, written through a buffered batcher
//	GET  /v1/games/stats     daily rollups (events, DAU, value) read from the rollup table
//	GET  /healthz            liveness
//	GET  /metrics            Prometheus text exposition
//
// Rollups are computed in the database, not in the application: a single INSERT ... SELECT
// GROUP BY does the work in one round trip and stays correct if two instances run at once.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lasttoss/go-log-api/internal/config"
	"github.com/lasttoss/go-log-api/internal/ingest"
	"github.com/lasttoss/go-log-api/internal/metrics"
	"github.com/lasttoss/go-log-api/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("bad configuration", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("cannot reach the database", "err", err)
		os.Exit(1)
	}
	defer s.Close()

	if err := s.Migrate(ctx); err != nil {
		logger.Error("migration failed", "err", err)
		os.Exit(1)
	}

	counters := metrics.New()
	batcher := ingest.NewBatcher(s, logger, cfg.BatchSize, cfg.FlushInterval, counters)

	svc := &ingest.Service{
		Reader:   s,
		Batcher:  batcher,
		Counters: counters,
		MaxBatch: cfg.MaxBatch,
		Logger:   logger,
	}
	rollup := store.NewRoller(s, logger, cfg.RollupInterval, counters)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/events", svc.HandleIngest)
	mux.HandleFunc("GET /v1/games/stats", svc.HandleStats)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Ping(r.Context()); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ok"))
	})
	mux.Handle("GET /metrics", counters.Handler())

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	go batcher.Run(ctx)
	go rollup.Run(ctx)

	go func() {
		logger.Info("listening", "addr", cfg.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
	batcher.Flush(shutdownCtx)
}

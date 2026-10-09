package config

import (
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("ADDR", "")
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/x")
	t.Setenv("BATCH_SIZE", "")
	t.Setenv("MAX_BATCH", "")
	t.Setenv("FLUSH_INTERVAL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr != ":8080" {
		t.Fatalf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.BatchSize != 500 || cfg.MaxBatch != 1000 {
		t.Fatalf("batch sizes = %d/%d, want 500/1000", cfg.BatchSize, cfg.MaxBatch)
	}
	if cfg.FlushInterval != 2*time.Second {
		t.Fatalf("FlushInterval = %v, want 2s", cfg.FlushInterval)
	}
}

func TestLoad_EnvironmentWins(t *testing.T) {
	t.Setenv("ADDR", ":9999")
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/x")
	t.Setenv("BATCH_SIZE", "10")
	t.Setenv("MAX_BATCH", "20")
	t.Setenv("FLUSH_INTERVAL", "250ms")
	t.Setenv("ROLLUP_INTERVAL", "1m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr != ":9999" || cfg.BatchSize != 10 || cfg.MaxBatch != 20 {
		t.Fatalf("environment was ignored: %+v", cfg)
	}
	if cfg.FlushInterval != 250*time.Millisecond || cfg.RollupInterval != time.Minute {
		t.Fatalf("durations were ignored: %+v", cfg)
	}
}

func TestLoad_RejectsABatchSizeAboveTheLimit(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/x")
	t.Setenv("BATCH_SIZE", "50")
	t.Setenv("MAX_BATCH", "10")

	if _, err := Load(); err == nil {
		t.Fatal("Load() accepted BATCH_SIZE > MAX_BATCH")
	}
}

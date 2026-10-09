package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config is the whole runtime configuration, read once at boot.
type Config struct {
	Addr           string
	DatabaseURL    string
	BatchSize      int
	MaxBatch       int
	FlushInterval  time.Duration
	RollupInterval time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Addr:           env("ADDR", ":8080"),
		DatabaseURL:    env("DATABASE_URL", "postgres://postgres:localdb@localhost:5432/gamelogs?sslmode=disable"),
		BatchSize:      envInt("BATCH_SIZE", 500),
		MaxBatch:       envInt("MAX_BATCH", 1000),
		FlushInterval:  envDuration("FLUSH_INTERVAL", 2*time.Second),
		RollupInterval: envDuration("ROLLUP_INTERVAL", 30*time.Second),
	}

	if cfg.BatchSize < 1 || cfg.MaxBatch < cfg.BatchSize {
		return Config{}, fmt.Errorf("BATCH_SIZE must be >= 1 and <= MAX_BATCH (got %d, %d)", cfg.BatchSize, cfg.MaxBatch)
	}
	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}

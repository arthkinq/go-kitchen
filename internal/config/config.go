package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config encapsulates runtime configuration for the Go.Kitchen service.
type Config struct {
	Port                string
	DatabaseURL         string
	LogLevel            slog.Level
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	ShutdownTimeout     time.Duration
	WebhookTimeout      time.Duration
	WebhookMaxInFlight  int
	AdminBootstrapToken string
}

// Load parses environment variables and applies default fallback values.
func Load() (*Config, error) {
	cfg := &Config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/go_kitchen?sslmode=disable"),
		LogLevel:    parseLogLevel(getEnv("LOG_LEVEL", "info")),

		AdminBootstrapToken: getEnv("ADMIN_BOOTSTRAP_TOKEN", ""),
	}

	maxInFlightRaw := getEnv("WEBHOOK_MAX_INFLIGHT", "256")
	maxInFlight, err := strconv.Atoi(maxInFlightRaw)
	if err != nil || maxInFlight <= 0 {
		return nil, fmt.Errorf("invalid WEBHOOK_MAX_INFLIGHT value %q: must be a positive integer", maxInFlightRaw)
	}
	cfg.WebhookMaxInFlight = maxInFlight

	durations := []struct {
		key    string
		defval string
		target *time.Duration
	}{
		{"READ_TIMEOUT", "15s", &cfg.ReadTimeout},
		{"WRITE_TIMEOUT", "15s", &cfg.WriteTimeout},
		{"IDLE_TIMEOUT", "60s", &cfg.IdleTimeout},
		{"SHUTDOWN_TIMEOUT", "10s", &cfg.ShutdownTimeout},
		{"WEBHOOK_TIMEOUT", "5s", &cfg.WebhookTimeout},
	}

	for _, d := range durations {
		raw := getEnv(d.key, d.defval)
		value, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid %s value %q: %w", d.key, raw, err)
		}
		if value <= 0 {
			return nil, fmt.Errorf("invalid %s value %q: must be positive", d.key, raw)
		}
		*d.target = value
	}

	return cfg, nil
}

// parseLogLevel maps a textual level to slog, defaulting to info for unknown values.
func parseLogLevel(raw string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

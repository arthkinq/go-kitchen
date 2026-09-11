package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("expected default port 8080, got %s", cfg.Port)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("expected default level info, got %s", cfg.LogLevel)
	}
	if cfg.ReadTimeout != 15*time.Second || cfg.WriteTimeout != 15*time.Second || cfg.IdleTimeout != 60*time.Second {
		t.Errorf("unexpected default timeouts: read=%s write=%s idle=%s",
			cfg.ReadTimeout, cfg.WriteTimeout, cfg.IdleTimeout)
	}
	if cfg.ShutdownTimeout != 10*time.Second || cfg.WebhookTimeout != 5*time.Second {
		t.Errorf("unexpected default timeouts: shutdown=%s webhook=%s", cfg.ShutdownTimeout, cfg.WebhookTimeout)
	}
}

// TestLoad_EnvIsHonoured pins down the contract advertised by docker-compose: every variable
// it sets must actually reach the running service.
func TestLoad_EnvIsHonoured(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("READ_TIMEOUT", "7s")
	t.Setenv("WRITE_TIMEOUT", "8s")
	t.Setenv("IDLE_TIMEOUT", "45s")
	t.Setenv("SHUTDOWN_TIMEOUT", "9s")
	t.Setenv("WEBHOOK_TIMEOUT", "3s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.Port != "9090" {
		t.Errorf("expected port 9090, got %s", cfg.Port)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("expected level debug, got %s", cfg.LogLevel)
	}
	if cfg.ReadTimeout != 7*time.Second {
		t.Errorf("expected read timeout 7s, got %s", cfg.ReadTimeout)
	}
	if cfg.WriteTimeout != 8*time.Second {
		t.Errorf("expected write timeout 8s, got %s", cfg.WriteTimeout)
	}
	if cfg.IdleTimeout != 45*time.Second {
		t.Errorf("expected idle timeout 45s, got %s", cfg.IdleTimeout)
	}
	if cfg.ShutdownTimeout != 9*time.Second {
		t.Errorf("expected shutdown timeout 9s, got %s", cfg.ShutdownTimeout)
	}
	if cfg.WebhookTimeout != 3*time.Second {
		t.Errorf("expected webhook timeout 3s, got %s", cfg.WebhookTimeout)
	}
}

func TestLoad_RejectsBadDurations(t *testing.T) {
	for _, value := range []string{"nonsense", "0s", "-1s"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("READ_TIMEOUT", value)
			if _, err := Load(); err == nil {
				t.Errorf("expected an error for READ_TIMEOUT=%q", value)
			}
		})
	}
}

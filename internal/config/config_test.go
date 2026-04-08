package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDotEnv(t *testing.T) {
	t.Setenv("EXISTING", "keep")

	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("FOO=bar\nEXISTING=override\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	if err := LoadDotEnv(path); err != nil {
		t.Fatalf("LoadDotEnv returned error: %v", err)
	}

	if got := os.Getenv("FOO"); got != "bar" {
		t.Fatalf("unexpected FOO: %s", got)
	}
	if got := os.Getenv("EXISTING"); got != "keep" {
		t.Fatalf("expected existing env var to win, got: %s", got)
	}
}

func TestReadDotEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("A=1\nB='two'\n# comment\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	values, err := ReadDotEnv(path)
	if err != nil {
		t.Fatalf("ReadDotEnv returned error: %v", err)
	}

	if values["A"] != "1" || values["B"] != "two" {
		t.Fatalf("unexpected values: %+v", values)
	}
}

func TestLoad(t *testing.T) {
	t.Setenv("CRYPTOBOT_DATABASE_URL", "postgres://example")
	t.Setenv("CRYPTOBOT_API_KEY", "key")
	t.Setenv("CRYPTOBOT_API_SECRET_KEY", "secret")
	t.Setenv("CRYPTOBOT_ADMIN_USERNAME", "admin")
	t.Setenv("CRYPTOBOT_ADMIN_PASSWORD", "pass")
	t.Setenv("CRYPTOBOT_LOG_LEVEL", "debug")
	t.Setenv("CRYPTOBOT_DRY_RUN", "true")
	t.Setenv("CRYPTOBOT_PRICE_SYNC_INTERVAL", "30m")
	t.Setenv("CRYPTOBOT_ORDER_RECONCILE_INTERVAL", "2m")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.DatabaseURL != "postgres://example" {
		t.Fatalf("unexpected database url: %s", cfg.DatabaseURL)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Fatalf("unexpected http addr: %s", cfg.HTTPAddr)
	}
	if cfg.AdminUsername != "admin" || cfg.AdminPassword != "pass" {
		t.Fatalf("unexpected admin credentials: %+v", cfg)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Fatalf("unexpected log level: %v", cfg.LogLevel)
	}
	if !cfg.DryRun {
		t.Fatal("expected dry run to be enabled")
	}
	if cfg.PriceSyncInterval != 30*time.Minute {
		t.Fatalf("unexpected price sync interval: %s", cfg.PriceSyncInterval)
	}
	if cfg.OrderReconcileInterval != 2*time.Minute {
		t.Fatalf("unexpected order reconcile interval: %s", cfg.OrderReconcileInterval)
	}
}

func TestParseBool(t *testing.T) {
	value, err := ParseBool("true")
	if err != nil {
		t.Fatalf("ParseBool returned error: %v", err)
	}
	if !value {
		t.Fatal("expected true")
	}
}

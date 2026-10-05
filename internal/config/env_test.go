package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestEnvOverridesFile(t *testing.T) {
	p := writeConfig(t, `
downly:
  telegram:
    bot_token: "from-file"
  database:
    postgres_url: "postgres://file"
  admin:
    user_ids: [1]
`)
	t.Setenv(EnvBotToken, "from-env")
	t.Setenv(EnvPostgresURL, "postgres://env")
	t.Setenv(EnvAdminIDs, "10, 20,")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Downly.Telegram.BotToken != "from-env" || cfg.Downly.Database.PostgresURL != "postgres://env" {
		t.Fatalf("env not applied: %+v", cfg.Downly)
	}
	if ids := cfg.Downly.Admin.UserIDs; len(ids) != 2 || ids[0] != 10 || ids[1] != 20 {
		t.Fatalf("admin ids = %v", ids)
	}
}

func TestEnvOnlyWithoutFile(t *testing.T) {
	t.Setenv(EnvBotToken, "tok")
	t.Setenv(EnvPostgresURL, "postgres://env")
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("env-only config should load: %v", err)
	}
	if cfg.Downly.Worker.NumberOfWorkers != 2 {
		t.Fatal("defaults not applied")
	}
}

func TestRequiredFields(t *testing.T) {
	p := writeConfig(t, "downly:\n  database:\n    postgres_url: x\n")
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "bot token") {
		t.Fatalf("missing token should fail, got %v", err)
	}
	p = writeConfig(t, "downly:\n  telegram:\n    bot_token: x\n")
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "postgres") {
		t.Fatalf("missing postgres url should fail, got %v", err)
	}
}

func TestBadAdminIDsEnv(t *testing.T) {
	t.Setenv(EnvBotToken, "tok")
	t.Setenv(EnvPostgresURL, "postgres://env")
	t.Setenv(EnvAdminIDs, "12,abc")
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("invalid admin id should fail")
	}
}

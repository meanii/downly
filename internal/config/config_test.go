package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	content := `
downly:
  telegram:
    bot_token: "test-token"
  database:
    postgres_url: "postgresql://test:test@localhost/test"
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Downly.Telegram.BotToken != "test-token" {
		t.Errorf("expected bot_token 'test-token', got %q", cfg.Downly.Telegram.BotToken)
	}
	if cfg.Downly.Worker.NumberOfWorkers != 2 {
		t.Errorf("expected default workers 2, got %d", cfg.Downly.Worker.NumberOfWorkers)
	}
	if cfg.Downly.Worker.PollIntervalSec != 3 {
		t.Errorf("expected default poll interval 3, got %d", cfg.Downly.Worker.PollIntervalSec)
	}
	if cfg.Downly.Worker.WorkDir != "./tmp" {
		t.Errorf("expected default work dir './tmp', got %q", cfg.Downly.Worker.WorkDir)
	}
	if cfg.Downly.Worker.MaxFileSizeMB != 45 {
		t.Errorf("expected default max file size 45, got %d", cfg.Downly.Worker.MaxFileSizeMB)
	}
	if cfg.Downly.Worker.StuckJobMinutes != 15 {
		t.Errorf("expected default stuck job minutes 15, got %d", cfg.Downly.Worker.StuckJobMinutes)
	}
	if cfg.Downly.Worker.HealthPort != 8080 {
		t.Errorf("expected default health port 8080, got %d", cfg.Downly.Worker.HealthPort)
	}
	if cfg.Downly.Services.YTDLP.Bin != "yt-dlp" {
		t.Errorf("expected default bin 'yt-dlp', got %q", cfg.Downly.Services.YTDLP.Bin)
	}
	if cfg.Downly.Services.YTDLP.AutoUpdateHours != 6 {
		t.Errorf("expected default auto update hours 6, got %d", cfg.Downly.Services.YTDLP.AutoUpdateHours)
	}
	if cfg.Downly.Limits.MaxQueuedPerUser != 5 {
		t.Errorf("expected default max queued 5, got %d", cfg.Downly.Limits.MaxQueuedPerUser)
	}
	if cfg.Downly.Limits.MaxConcurrentPerUser != 2 {
		t.Errorf("expected default max concurrent 2, got %d", cfg.Downly.Limits.MaxConcurrentPerUser)
	}
	if cfg.Downly.Limits.RateLimitSeconds != 10 {
		t.Errorf("expected default rate limit 10, got %d", cfg.Downly.Limits.RateLimitSeconds)
	}
	if cfg.Downly.Limits.MaxRetries != 1 {
		t.Errorf("expected default max retries 1, got %d", cfg.Downly.Limits.MaxRetries)
	}
	if cfg.Downly.Cleanup.RetentionHours != 72 {
		t.Errorf("expected default retention hours 72, got %d", cfg.Downly.Cleanup.RetentionHours)
	}
}

func TestLoadOverrides(t *testing.T) {
	content := `
downly:
  telegram:
    bot_token: "prod-token"
  database:
    postgres_url: "postgresql://prod:prod@db/prod"
  worker:
    numbers_of_workers: 4
    poll_interval_sec: 5
    max_file_size_mb: 100
    stuck_job_minutes: 30
    health_port: 9090
  limits:
    max_queued_per_user: 10
    rate_limit_seconds: 5
    max_retries: 3
  services:
    ytdl:
      bin: "/usr/bin/yt-dlp"
      auto_update_hours: 12
`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Downly.Worker.NumberOfWorkers != 4 {
		t.Errorf("expected workers 4, got %d", cfg.Downly.Worker.NumberOfWorkers)
	}
	if cfg.Downly.Worker.StuckJobMinutes != 30 {
		t.Errorf("expected stuck 30, got %d", cfg.Downly.Worker.StuckJobMinutes)
	}
	if cfg.Downly.Worker.HealthPort != 9090 {
		t.Errorf("expected health port 9090, got %d", cfg.Downly.Worker.HealthPort)
	}
	if cfg.Downly.Limits.RateLimitSeconds != 5 {
		t.Errorf("expected rate limit 5, got %d", cfg.Downly.Limits.RateLimitSeconds)
	}
	if cfg.Downly.Limits.MaxRetries != 3 {
		t.Errorf("expected max retries 3, got %d", cfg.Downly.Limits.MaxRetries)
	}
	if cfg.Downly.Services.YTDLP.AutoUpdateHours != 12 {
		t.Errorf("expected auto update 12, got %d", cfg.Downly.Services.YTDLP.AutoUpdateHours)
	}
}

func TestLoadInvalidPath(t *testing.T) {
	_, err := Load("/nonexistent/config.yaml")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte(":::invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

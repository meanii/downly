package config

import (
	"strings"
	"testing"
)

func TestLocalAPIDefaults(t *testing.T) {
	cloud, err := Load(writeConfig(t, "downly:\n  telegram:\n    bot_token: x\n  database:\n    postgres_url: y\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cloud.UsesLocalAPI() || cloud.Downly.Worker.MaxFileSizeMB != 45 || len(cloud.Warnings()) != 0 {
		t.Fatalf("cloud defaults: %+v %v", cloud.Downly.Worker, cloud.Warnings())
	}

	t.Setenv(EnvAPIURL, "http://telegram-bot-api:8081")
	local, err := Load(writeConfig(t, "downly:\n  telegram:\n    bot_token: x\n  database:\n    postgres_url: y\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !local.UsesLocalAPI() || local.Downly.Telegram.APIURL != "http://telegram-bot-api:8081" {
		t.Fatal("DOWNLY_API_URL not applied")
	}
	if local.Downly.Worker.MaxFileSizeMB != LocalAPIMaxFileMB || local.Downly.Worker.MaxDownloadMB != 2*LocalAPIMaxFileMB {
		t.Fatalf("local defaults: %+v", local.Downly.Worker)
	}
	if len(local.Warnings()) != 0 {
		t.Fatalf("unexpected warnings: %v", local.Warnings())
	}
}

func TestLocalAPIWarnings(t *testing.T) {
	c, err := Load(writeConfig(t, "downly:\n  telegram:\n    bot_token: x\n  database:\n    postgres_url: y\n  worker:\n    max_file_size_mb: 1900\n"))
	if err != nil {
		t.Fatal(err)
	}
	if w := c.Warnings(); len(w) != 1 || !strings.Contains(w[0], "api_url") {
		t.Fatalf("warnings = %v", w)
	}
	c.Downly.Telegram.APIURL = "http://x"
	c.Downly.Worker.MaxFileSizeMB = 4000
	if w := c.Warnings(); len(w) != 1 || !strings.Contains(w[0], "2000MB") {
		t.Fatalf("warnings = %v", w)
	}
}

package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Root struct {
	Downly Downly `yaml:"downly"`
}

type Downly struct {
	Telegram Telegram `yaml:"telegram"`
	Database Database `yaml:"database"`
	Worker   Worker   `yaml:"worker"`
	Services Services `yaml:"services"`
	Limits   Limits   `yaml:"limits"`
	Admin    Admin    `yaml:"admin"`
	Cleanup  Cleanup  `yaml:"cleanup"`
}

type Telegram struct {
	BotToken string `yaml:"bot_token"`
}

type Database struct {
	PostgresURL string `yaml:"postgres_url"`
}

type Worker struct {
	NumberOfWorkers int    `yaml:"numbers_of_workers"`
	PollIntervalSec int    `yaml:"poll_interval_sec"`
	WorkDir         string `yaml:"work_dir"`
	MaxFileSizeMB   int64  `yaml:"max_file_size_mb"`
	StuckJobMinutes int    `yaml:"stuck_job_minutes"`
	HealthPort      int    `yaml:"health_port"`
}

type Services struct {
	YTDLP YTDLP `yaml:"ytdl"`
}

type YTDLP struct {
	Enabled             bool   `yaml:"enabled"`
	Bin                 string `yaml:"bin"`
	CookiesFile         string `yaml:"cookies_file"`
	AutoUpdateHours     int    `yaml:"auto_update_hours"`
}

type Limits struct {
	MaxQueuedPerUser     int `yaml:"max_queued_per_user"`
	MaxConcurrentPerUser int `yaml:"max_concurrent_per_user"`
	RateLimitSeconds     int `yaml:"rate_limit_seconds"`
	MaxRetries           int `yaml:"max_retries"`
}

type Admin struct {
	UserIDs []int64 `yaml:"user_ids"`
}

type Cleanup struct {
	Enabled        bool `yaml:"enabled"`
	RetentionHours int  `yaml:"retention_hours"`
}

func Load(path string) (*Root, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Root
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Downly.Worker.NumberOfWorkers <= 0 {
		cfg.Downly.Worker.NumberOfWorkers = 2
	}
	if cfg.Downly.Worker.PollIntervalSec <= 0 {
		cfg.Downly.Worker.PollIntervalSec = 3
	}
	if cfg.Downly.Worker.WorkDir == "" {
		cfg.Downly.Worker.WorkDir = "./tmp"
	}
	if cfg.Downly.Worker.MaxFileSizeMB <= 0 {
		cfg.Downly.Worker.MaxFileSizeMB = 45
	}
	if cfg.Downly.Worker.StuckJobMinutes <= 0 {
		cfg.Downly.Worker.StuckJobMinutes = 15
	}
	if cfg.Downly.Worker.HealthPort <= 0 {
		cfg.Downly.Worker.HealthPort = 8080
	}
	if cfg.Downly.Services.YTDLP.Bin == "" {
		cfg.Downly.Services.YTDLP.Bin = "yt-dlp"
	}
	if cfg.Downly.Limits.MaxQueuedPerUser <= 0 {
		cfg.Downly.Limits.MaxQueuedPerUser = 5
	}
	if cfg.Downly.Limits.MaxConcurrentPerUser <= 0 {
		cfg.Downly.Limits.MaxConcurrentPerUser = 2
	}
	if cfg.Downly.Services.YTDLP.AutoUpdateHours <= 0 {
		cfg.Downly.Services.YTDLP.AutoUpdateHours = 6
	}
	if cfg.Downly.Limits.RateLimitSeconds <= 0 {
		cfg.Downly.Limits.RateLimitSeconds = 10
	}
	if cfg.Downly.Limits.MaxRetries <= 0 {
		cfg.Downly.Limits.MaxRetries = 1
	}
	if cfg.Downly.Cleanup.RetentionHours <= 0 {
		cfg.Downly.Cleanup.RetentionHours = 72
	}
	return &cfg, nil
}

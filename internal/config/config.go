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
}

type Telegram struct {
	BotToken string `yaml:"bot_token"`
}

type Database struct {
	PostgresURL string `yaml:"postgres_url"`
}

type Worker struct {
	NumberOfWorkers int   `yaml:"numbers_of_workers"`
	PollIntervalSec int   `yaml:"poll_interval_sec"`
	WorkDir         string `yaml:"work_dir"`
	MaxFileSizeMB   int64 `yaml:"max_file_size_mb"`
}

type Services struct {
	YTDLP YTDLP `yaml:"ytdl"`
}

type YTDLP struct {
	Enabled bool   `yaml:"enabled"`
	Bin     string `yaml:"bin"`
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
	if cfg.Downly.Services.YTDLP.Bin == "" {
		cfg.Downly.Services.YTDLP.Bin = "yt-dlp"
	}
	return &cfg, nil
}

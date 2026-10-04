package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

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
	Cache    Cache    `yaml:"cache"`
	// Subscriptions controls /follow.
	Subscriptions Subscriptions `yaml:"subscriptions"`
}

type Telegram struct {
	BotToken string `yaml:"bot_token"`
	// APIURL points at a self-hosted Bot API server (raises the upload limit
	// to 2GB). Empty means https://api.telegram.org.
	APIURL string `yaml:"api_url"`
}

type Database struct {
	PostgresURL string `yaml:"postgres_url"`
}

type Worker struct {
	NumberOfWorkers int    `yaml:"numbers_of_workers"`
	PollIntervalSec int    `yaml:"poll_interval_sec"`
	WorkDir         string `yaml:"work_dir"`
	MaxFileSizeMB   int64  `yaml:"max_file_size_mb"`
	// MaxDownloadMB is the largest source we fetch and then compress to fit
	// MaxFileSizeMB. Defaults to 4x MaxFileSizeMB.
	MaxDownloadMB int64 `yaml:"max_download_size_mb"`
	// StuckJobMinutes: a processing job whose heartbeat is older than this
	// is considered dead and is requeued (or failed when out of retries).
	StuckJobMinutes int `yaml:"stuck_job_minutes"`
	// JobTimeoutMinutes caps download + processing time for one attempt.
	JobTimeoutMinutes int `yaml:"job_timeout_minutes"`
	// ShutdownGraceSec is how long running jobs may finish after SIGTERM
	// before they are interrupted and requeued.
	ShutdownGraceSec int `yaml:"shutdown_grace_seconds"`
	HealthPort       int `yaml:"health_port"`
}

type Services struct {
	YTDLP YTDLP `yaml:"ytdl"`
}

type YTDLP struct {
	Enabled         bool   `yaml:"enabled"`
	Bin             string `yaml:"bin"`
	CookiesFile     string `yaml:"cookies_file"`
	AutoUpdateHours int    `yaml:"auto_update_hours"`
	// AlbumHosts overrides the sites whose multi-photo/video posts are sent
	// as albums (default: downloader.DefaultAlbumHosts).
	AlbumHosts []string `yaml:"album_hosts"`
}

type Limits struct {
	MaxQueuedPerUser     int `yaml:"max_queued_per_user"`
	MaxConcurrentPerUser int `yaml:"max_concurrent_per_user"`
	RateLimitSeconds     int `yaml:"rate_limit_seconds"`
	MaxRetries           int `yaml:"max_retries"`
	DailyQuotaPerUser    int `yaml:"daily_quota_per_user"`
}

type Admin struct {
	UserIDs        []int64 `yaml:"user_ids"`
	StatsChannelID int64   `yaml:"stats_channel_id"`
	StatsIntervalH int     `yaml:"stats_interval_hours"`
}

// Cache controls re-sending finished downloads by Telegram file ID.
type Cache struct {
	Disabled bool `yaml:"disabled"`
	// RetentionDays drops entries unused for this long (default 60).
	RetentionDays int `yaml:"retention_days"`
}

// Subscriptions controls following channels and playlists.
type Subscriptions struct {
	Disabled bool `yaml:"disabled"`
	// IntervalMinutes between checks of each feed (default 60).
	IntervalMinutes int `yaml:"interval_minutes"`
	// MaxPerChat caps feeds per chat (default 5).
	MaxPerChat int `yaml:"max_per_chat"`
}

type Cleanup struct {
	Enabled        bool `yaml:"enabled"`
	RetentionHours int  `yaml:"retention_hours"`
}

// Environment variables that override (or replace) config file values, so
// secrets need not live in config.yaml.
const (
	EnvBotToken    = "DOWNLY_BOT_TOKEN"
	EnvPostgresURL = "DOWNLY_POSTGRES_URL"
	EnvAdminIDs    = "DOWNLY_ADMIN_IDS" // comma-separated Telegram user IDs
)

// Load reads the YAML config at path, applies environment overrides and
// defaults, and validates required fields. The file may be absent when the
// required values come from the environment.
func Load(path string) (*Root, error) {
	var cfg Root
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("parse config: %w", err)
		}
	case os.IsNotExist(err) && os.Getenv(EnvBotToken) != "":
		// Fully environment-driven deployment.
	default:
		return nil, fmt.Errorf("read config: %w", err)
	}
	if err := applyEnv(&cfg); err != nil {
		return nil, err
	}
	if cfg.Downly.Telegram.BotToken == "" {
		return nil, fmt.Errorf("telegram bot token is required (downly.telegram.bot_token or %s)", EnvBotToken)
	}
	if cfg.Downly.Database.PostgresURL == "" {
		return nil, fmt.Errorf("postgres URL is required (downly.database.postgres_url or %s)", EnvPostgresURL)
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
	if cfg.Downly.Worker.MaxDownloadMB <= 0 {
		cfg.Downly.Worker.MaxDownloadMB = 4 * cfg.Downly.Worker.MaxFileSizeMB
	}
	if cfg.Downly.Worker.MaxDownloadMB < cfg.Downly.Worker.MaxFileSizeMB {
		cfg.Downly.Worker.MaxDownloadMB = cfg.Downly.Worker.MaxFileSizeMB
	}
	if cfg.Downly.Worker.StuckJobMinutes <= 0 {
		cfg.Downly.Worker.StuckJobMinutes = 5
	}
	if cfg.Downly.Worker.JobTimeoutMinutes <= 0 {
		cfg.Downly.Worker.JobTimeoutMinutes = 30
	}
	if cfg.Downly.Worker.ShutdownGraceSec <= 0 {
		cfg.Downly.Worker.ShutdownGraceSec = 60
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
	// DailyQuotaPerUser: 0 means unlimited (no default override needed)

	if cfg.Downly.Cleanup.RetentionHours <= 0 {
		cfg.Downly.Cleanup.RetentionHours = 72
	}
	if cfg.Downly.Subscriptions.IntervalMinutes <= 0 {
		cfg.Downly.Subscriptions.IntervalMinutes = 60
	}
	if cfg.Downly.Subscriptions.MaxPerChat <= 0 {
		cfg.Downly.Subscriptions.MaxPerChat = 5
	}
	if cfg.Downly.Cache.RetentionDays <= 0 {
		cfg.Downly.Cache.RetentionDays = 60
	}
	if cfg.Downly.Admin.StatsIntervalH <= 0 {
		cfg.Downly.Admin.StatsIntervalH = 24
	}
	return &cfg, nil
}

func applyEnv(cfg *Root) error {
	if v := os.Getenv(EnvBotToken); v != "" {
		cfg.Downly.Telegram.BotToken = v
	}
	if v := os.Getenv(EnvPostgresURL); v != "" {
		cfg.Downly.Database.PostgresURL = v
	}
	if v := os.Getenv(EnvAdminIDs); v != "" {
		var ids []int64
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := strconv.ParseInt(part, 10, 64)
			if err != nil {
				return fmt.Errorf("parse %s: %q is not a user ID", EnvAdminIDs, part)
			}
			ids = append(ids, id)
		}
		cfg.Downly.Admin.UserIDs = ids
	}
	return nil
}

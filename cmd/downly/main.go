package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-telegram/bot"
	"github.com/jackc/pgx/v5/pgxpool"

	cfgpkg "github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/cleanup"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/logging"
	mig "github.com/meanii/downly/internal/migrate"
	"github.com/meanii/downly/internal/reaper"
	tgbot "github.com/meanii/downly/internal/telegram"
	"github.com/meanii/downly/internal/updater"
	"github.com/meanii/downly/internal/worker"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	logger := logging.New()
	slog.SetDefault(logger)
	logger.Info("starting downly", "config_path", *configPath)

	cfg, err := cfgpkg.Load(*configPath)
	if err != nil {
		logger.Error("load config failed", "error", err)
		os.Exit(1)
	}

	// Root context with cancellation for graceful shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.Downly.Database.PostgresURL)
	if err != nil {
		logger.Error("connect postgres failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	logger.Info("postgres pool created")

	if err := mig.Up(cfg.Downly.Database.PostgresURL); err != nil {
		logger.Error("run migrations failed", "error", err)
		os.Exit(1)
	}
	if err := db.EnsureSchema(ctx, pool); err != nil {
		logger.Error("ensure schema compatibility failed", "error", err)
		os.Exit(1)
	}
	logger.Info("schema ready")

	if err := os.MkdirAll(cfg.Downly.Worker.WorkDir, 0o755); err != nil {
		logger.Error("mkdir work dir failed", "work_dir", cfg.Downly.Worker.WorkDir, "error", err)
		os.Exit(1)
	}

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   15 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     false,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
			MaxIdleConnsPerHost:   10,
			ResponseHeaderTimeout: 70 * time.Second,
		},
	}

	b, err := bot.New(cfg.Downly.Telegram.BotToken, bot.WithHTTPClient(30*time.Second, httpClient))
	if err != nil {
		logger.Error("init telegram bot failed", "error", err)
		os.Exit(1)
	}
	logger.Info("telegram bot initialized")

	controller := worker.NewController()
	tgbot.RegisterHandlers(logger, cfg, controller, b, pool)

	// Background services
	go cleanup.Loop(ctx, logger, pool, cfg.Downly.Cleanup.Enabled, cfg.Downly.Cleanup.RetentionHours)
	go updater.Loop(ctx, logger, cfg.Downly.Services.YTDLP.Bin, cfg.Downly.Services.YTDLP.AutoUpdateHours)
	go reaper.Loop(ctx, logger, pool, cfg.Downly.Worker.StuckJobMinutes)

	// Health check endpoint
	go startHealthServer(logger, pool, cfg.Downly.Worker.HealthPort)

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < cfg.Downly.Worker.NumberOfWorkers; i++ {
		workerID := i + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Loop(ctx, logger, controller, workerID, cfg, pool, b)
		}()
	}

	// Start telegram polling in a goroutine so we can handle shutdown
	go b.Start(ctx)
	logger.Info("downly is running", "workers", cfg.Downly.Worker.NumberOfWorkers)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("received shutdown signal", "signal", sig.String())

	// Cancel context to stop all workers and background services
	cancel()
	logger.Info("waiting for workers to finish current jobs...")

	// Give workers a deadline to finish
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("all workers stopped cleanly")
	case <-time.After(2 * time.Minute):
		logger.Warn("shutdown deadline reached, forcing exit")
	}

	logger.Info("downly stopped")
}

func startHealthServer(logger *slog.Logger, pool *pgxpool.Pool, port int) {
	if port <= 0 {
		port = 8080
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if err := pool.Ping(r.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprintf(w, "db: %v", err)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	})

	addr := fmt.Sprintf(":%d", port)
	logger.Info("health endpoint started", "addr", addr)
	if err := http.ListenAndServe(addr, mux); err != nil {
		logger.Error("health server failed", "error", err)
	}
}

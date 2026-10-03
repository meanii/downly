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

	"github.com/meanii/downly/internal/cleanup"
	cfgpkg "github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/logging"
	mig "github.com/meanii/downly/internal/migrate"
	"github.com/meanii/downly/internal/reaper"
	"github.com/meanii/downly/internal/statsreport"
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
	logger.Info("schema ready")

	if err := os.MkdirAll(cfg.Downly.Worker.WorkDir, 0o755); err != nil {
		logger.Error("mkdir work dir failed", "work_dir", cfg.Downly.Worker.WorkDir, "error", err)
		os.Exit(1)
	}

	// No overall client Timeout: it would also cap file uploads, which can
	// legitimately take minutes. Calls are bounded by their contexts instead,
	// and ResponseHeaderTimeout catches a stalled server. It must exceed the
	// long-poll duration below.
	httpClient := &http.Client{
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
			ResponseHeaderTimeout: 90 * time.Second,
		},
	}

	b, err := bot.New(cfg.Downly.Telegram.BotToken,
		bot.WithHTTPClient(60*time.Second, httpClient),
		bot.WithMiddlewares(tgbot.UserTracker(pool, logger)),
	)
	if err != nil {
		logger.Error("init telegram bot failed", "error", err)
		os.Exit(1)
	}
	logger.Info("telegram bot initialized")

	controller := worker.NewController()
	tgbot.RegisterHandlers(logger, cfg, controller, b, pool)

	// Anything in the work dir older than one job timeout is from a crash.
	worker.SweepWorkDir(logger, cfg.Downly.Worker.WorkDir, time.Duration(cfg.Downly.Worker.JobTimeoutMinutes)*time.Minute)

	// Background services
	go cleanup.Loop(ctx, logger, pool, cfg.Downly.Cleanup.Enabled, cfg.Downly.Cleanup.RetentionHours)
	go updater.Loop(ctx, logger, cfg.Downly.Services.YTDLP.Bin, cfg.Downly.Services.YTDLP.AutoUpdateHours)
	go reaper.Loop(ctx, logger, pool, cfg.Downly.Worker.StuckJobMinutes, cfg.Downly.Limits.MaxRetries)
	go statsreport.Loop(ctx, logger, pool, b, cfg.Downly.Admin.StatsChannelID, cfg.Downly.Admin.StatsIntervalH)

	// Health check endpoint
	go startHealthServer(logger, pool, cfg.Downly.Worker.HealthPort)

	// Workers. Shutdown happens in two phases: claimCtx stops taking new
	// jobs, workCtx interrupts (and requeues) jobs still running after the
	// grace period.
	claimCtx, stopClaiming := context.WithCancel(ctx)
	workCtx, stopWork := context.WithCancel(context.Background())
	defer stopWork()

	waker := worker.NewWaker()
	go worker.Listen(claimCtx, logger, pool, waker)

	dl := downloader.YTDLP{
		Bin:           cfg.Downly.Services.YTDLP.Bin,
		CookiesFile:   cfg.Downly.Services.YTDLP.CookiesFile,
		MaxFileSizeMB: cfg.Downly.Worker.MaxFileSizeMB,
		MaxDownloadMB: cfg.Downly.Worker.MaxDownloadMB,
		Logger:        logger,
	}
	host, _ := os.Hostname()
	var wg sync.WaitGroup
	for i := 0; i < cfg.Downly.Worker.NumberOfWorkers; i++ {
		w := &worker.Worker{
			ID:         fmt.Sprintf("%s-%d-%d", host, os.Getpid(), i+1),
			Cfg:        cfg,
			Pool:       pool,
			DL:         dl,
			Msg:        worker.TelegramMessenger{Bot: b},
			Controller: controller,
			Waker:      waker,
			Log:        logger,
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.Run(claimCtx, workCtx)
		}()
	}

	// Telegram polling stops first on shutdown so no new jobs arrive.
	botCtx, stopBot := context.WithCancel(ctx)
	defer stopBot()
	go b.Start(botCtx)
	logger.Info("downly is running", "workers", cfg.Downly.Worker.NumberOfWorkers)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	logger.Info("received shutdown signal", "signal", sig.String())

	stopBot()
	stopClaiming()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	grace := time.Duration(cfg.Downly.Worker.ShutdownGraceSec) * time.Second
	logger.Info("waiting for running jobs to finish", "grace", grace)
	select {
	case <-done:
		logger.Info("all workers stopped cleanly")
	case <-time.After(grace):
		logger.Warn("grace period over, interrupting and requeueing running jobs")
		stopWork()
		select {
		case <-done:
			logger.Info("workers stopped after requeueing")
		case <-time.After(30 * time.Second):
			logger.Warn("workers did not stop in time; the reaper will recover their jobs")
		}
	case sig := <-sigCh:
		logger.Warn("second signal, interrupting running jobs", "signal", sig.String())
		stopWork()
		<-done
	}

	cancel()
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

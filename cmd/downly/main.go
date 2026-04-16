package main

import (
	"context"
	"flag"
	"os"
	"sync"

	"github.com/go-telegram/bot"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	cfgpkg "github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	tgbot "github.com/meanii/downly/internal/telegram"
	"github.com/meanii/downly/internal/worker"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})

	cfg, err := cfgpkg.Load(*configPath)
	if err != nil {
		log.Fatal().Err(err).Msg("load config")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, cfg.Downly.Database.PostgresURL)
	if err != nil {
		log.Fatal().Err(err).Msg("connect postgres")
	}
	defer pool.Close()

	if err := db.EnsureSchema(ctx, pool); err != nil {
		log.Fatal().Err(err).Msg("ensure schema")
	}
	if err := os.MkdirAll(cfg.Downly.Worker.WorkDir, 0o755); err != nil {
		log.Fatal().Err(err).Msg("mkdir work dir")
	}

	b, err := bot.New(cfg.Downly.Telegram.BotToken)
	if err != nil {
		log.Fatal().Err(err).Msg("init telegram bot")
	}
	tgbot.RegisterHandlers(b, pool)

	var wg sync.WaitGroup
	for i := 0; i < cfg.Downly.Worker.NumberOfWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			worker.Loop(ctx, cfg, pool, b)
		}()
	}

	b.Start(ctx)
	wg.Wait()
}

package reaper

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/db"
)

// Loop periodically recovers jobs whose worker stopped heartbeating (crash,
// OOM kill, network partition). It also runs once at startup so jobs left
// "processing" by a previous crash are picked up quickly.
func Loop(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, stuckMinutes, maxRetries int) {
	log := logger.With("component", "reaper")
	if stuckMinutes <= 0 {
		stuckMinutes = 5
	}
	staleAfter := time.Duration(stuckMinutes) * time.Minute
	interval := time.Minute
	if staleAfter < interval {
		interval = staleAfter
	}

	log.Info("dead job reaper started", "stale_after", staleAfter, "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		requeued, failed, err := db.ReapStuckJobs(ctx, pool, staleAfter, maxRetries)
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("reap stuck jobs failed", "error", err)
		case requeued > 0 || failed > 0:
			log.Warn("recovered stuck jobs", "requeued", requeued, "failed", failed)
		}

		select {
		case <-ctx.Done():
			log.Info("reaper stopped")
			return
		case <-ticker.C:
		}
	}
}

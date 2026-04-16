package reaper

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/db"
)

// Loop periodically checks for jobs stuck in "processing" and marks them failed.
// stuckMinutes is how long a job can stay in processing before being reaped.
func Loop(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, stuckMinutes int) {
	log := logger.With("component", "reaper")
	if stuckMinutes <= 0 {
		stuckMinutes = 15
	}
	interval := time.Duration(stuckMinutes) * time.Minute

	log.Info("dead job reaper started", "stuck_threshold_minutes", stuckMinutes)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("reaper stopped")
			return
		case <-ticker.C:
			reaped, err := db.ReapStuckJobs(ctx, pool, stuckMinutes)
			if err != nil {
				log.Error("reap stuck jobs failed", "error", err)
				continue
			}
			if reaped > 0 {
				log.Warn("reaped stuck jobs", "count", reaped)
			}
		}
	}
}

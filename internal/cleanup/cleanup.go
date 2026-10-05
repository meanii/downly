package cleanup

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/db"
)

// Loop prunes finished jobs older than retentionHours (when enabled) and
// media cache entries unused for cacheRetention, once an hour.
func Loop(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, enabled bool, retentionHours int, cacheRetention time.Duration) {
	log := logger.With("component", "cleanup")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		if enabled {
			if n, err := db.PruneJobs(ctx, pool, retentionHours); err != nil {
				log.Error("cleanup failed", "error", err)
			} else {
				log.Info("cleanup completed", "retention_hours", retentionHours, "pruned_jobs", n)
			}
		}
		if cacheRetention > 0 {
			if n, err := db.PruneCache(ctx, pool, cacheRetention); err != nil {
				log.Error("cache cleanup failed", "error", err)
			} else if n > 0 {
				log.Info("pruned media cache", "entries", n)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

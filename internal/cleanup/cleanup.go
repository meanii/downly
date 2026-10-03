package cleanup

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/db"
)

func Loop(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, enabled bool, retentionHours int) {
	if !enabled {
		return
	}
	log := logger.With("component", "cleanup")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		if n, err := db.PruneJobs(ctx, pool, retentionHours); err != nil {
			log.Error("cleanup failed", "error", err)
		} else {
			log.Info("cleanup completed", "retention_hours", retentionHours, "pruned_jobs", n)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

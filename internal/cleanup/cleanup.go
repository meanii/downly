package cleanup

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Loop(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, enabled bool, retentionHours int) {
	if !enabled {
		return
	}
	log := logger.With("component", "cleanup")
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		if err := runOnce(ctx, pool, retentionHours); err != nil {
			log.Error("cleanup failed", "error", err)
		} else {
			log.Info("cleanup completed", "retention_hours", retentionHours)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runOnce(ctx context.Context, pool *pgxpool.Pool, retentionHours int) error {
	_, err := pool.Exec(ctx, `
		delete from download_jobs
		where status in ('done', 'failed', 'canceled')
		and finished_at is not null
		and finished_at < now() - make_interval(hours => $1)
	`, retentionHours)
	return err
}

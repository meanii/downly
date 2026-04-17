package statsreport

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/db"
)

// Loop periodically posts bot stats to the configured admin channel.
func Loop(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, b *bot.Bot, channelID int64, intervalHours int) {
	log := logger.With("component", "statsreport")

	if channelID == 0 {
		log.Info("stats reporting disabled (no stats_channel_id configured)")
		return
	}
	if intervalHours <= 0 {
		intervalHours = 24
	}

	interval := time.Duration(intervalHours) * time.Hour
	log.Info("stats reporter started", "channel_id", channelID, "interval_hours", intervalHours)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("stats reporter stopped")
			return
		case <-ticker.C:
			report, err := buildReport(ctx, pool)
			if err != nil {
				log.Error("build stats report failed", "error", err)
				continue
			}
			_, err = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: channelID,
				Text:   report,
			})
			if err != nil {
				log.Error("send stats report failed", "error", err)
			} else {
				log.Info("stats report sent")
			}
		}
	}
}

func buildReport(ctx context.Context, pool *pgxpool.Pool) (string, error) {
	stats, err := db.GetBotStats(ctx, pool)
	if err != nil {
		return "", err
	}
	topUsers, _ := db.GetTopUsers(ctx, pool, 5)
	topPlatforms, _ := db.GetTopPlatforms(ctx, pool, 5)

	lines := []string{
		"-- Scheduled Stats Report --",
		"",
		fmt.Sprintf("Total jobs: %d", stats.TotalJobs),
		fmt.Sprintf("Completed: %d", stats.TotalDone),
		fmt.Sprintf("Failed: %d", stats.TotalFailed),
		fmt.Sprintf("Canceled: %d", stats.TotalCanceled),
		fmt.Sprintf("Pending: %d", stats.TotalPending),
		fmt.Sprintf("Active: %d", stats.TotalActive),
		fmt.Sprintf("Unique users: %d", stats.UniqueUsers),
	}

	if stats.TotalJobs > 0 {
		successRate := float64(stats.TotalDone) / float64(stats.TotalJobs) * 100
		lines = append(lines, fmt.Sprintf("Success rate: %.1f%%", successRate))
	}

	if len(topPlatforms) > 0 {
		lines = append(lines, "", "Top platforms:")
		for i, p := range topPlatforms {
			lines = append(lines, fmt.Sprintf("  %d. %s (%d)", i+1, p.Platform, p.Count))
		}
	}

	if len(topUsers) > 0 {
		lines = append(lines, "", "Top users:")
		for i, u := range topUsers {
			lines = append(lines, fmt.Sprintf("  %d. %d (%d jobs)", i+1, u.UserID, u.JobCount))
		}
	}

	lines = append(lines, "", fmt.Sprintf("Report generated at %s", time.Now().UTC().Format("2006-01-02 15:04 UTC")))

	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out, nil
}

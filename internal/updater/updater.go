package updater

import (
	"context"
	"log/slog"
	"os/exec"
	"strings"
	"time"
)

// Loop checks for yt-dlp updates at the given interval and applies them.
func Loop(ctx context.Context, logger *slog.Logger, bin string, intervalHours int) {
	log := logger.With("component", "updater")
	interval := time.Duration(intervalHours) * time.Hour
	if interval <= 0 {
		interval = 6 * time.Hour
	}

	log.Info("yt-dlp auto-updater started", "interval_hours", intervalHours, "bin", bin)

	// Run once at startup then on interval.
	update(log, bin)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("updater stopped")
			return
		case <-ticker.C:
			update(log, bin)
		}
	}
}

func update(log *slog.Logger, bin string) {
	log.Info("checking for yt-dlp update")
	out, err := exec.Command(bin, "-U").CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		log.Error("yt-dlp update failed", "error", err, "output", output)
		return
	}
	if strings.Contains(output, "Updated yt-dlp to") || strings.Contains(output, "Updating to") {
		log.Info("yt-dlp updated", "output", output)
	} else {
		log.Info("yt-dlp is up to date", "output", output)
	}
}

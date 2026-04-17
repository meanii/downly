package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
)

func Loop(ctx context.Context, logger *slog.Logger, controller *Controller, workerID int, cfg *config.Root, pool *pgxpool.Pool, b *bot.Bot) {
	poll := time.Duration(cfg.Downly.Worker.PollIntervalSec) * time.Second
	dl := downloader.YTDLP{Bin: cfg.Downly.Services.YTDLP.Bin, CookiesFile: cfg.Downly.Services.YTDLP.CookiesFile, MaxFileSizeMB: cfg.Downly.Worker.MaxFileSizeMB, Logger: logger}
	workerLog := logger.With("component", "worker", "worker_id", workerID)

	workerLog.Info("worker started", "poll_interval_sec", cfg.Downly.Worker.PollIntervalSec)
	for {
		select {
		case <-ctx.Done():
			workerLog.Info("worker stopped")
			return
		default:
		}

		job, err := db.ClaimJob(ctx, pool)
		if err != nil {
			workerLog.Error("claim job failed", "error", err)
			time.Sleep(poll)
			continue
		}
		if job == nil {
			time.Sleep(poll)
			continue
		}

		jobCtx, cancel := context.WithCancel(ctx)
		controller.Register(job.ID, cancel)
		jobLog := workerLog.With("job_id", job.ID, "chat_id", job.ChatID, "url", job.URL, "priority", job.Priority)
		jobLog.Info("job claimed")
		editProgress(jobCtx, b, job.ChatID, int(job.TelegramMsgID), formatProgressMessage(job.ID, job.Status, job.ProgressText, job.ProgressPercent, 0, 0, job.Priority))

		lastPercent := -1
		progressFn := func(text string, percent int) {
			if percent == lastPercent {
				return
			}
			lastPercent = percent
			_ = db.UpdateProgress(jobCtx, pool, job.ID, text, percent)
			editProgress(jobCtx, b, job.ChatID, int(job.TelegramMsgID), formatProgressMessage(job.ID, job.Status, text, percent, 0, 1, job.Priority))
		}

		// Choose download mode based on URL prefix marker
		var res *downloader.Result
		actualURL, mode, quality := parseJobURL(job.URL)
		switch mode {
		case "audio":
			res, err = dl.DownloadAudio(jobCtx, cfg.Downly.Worker.WorkDir, job.ID, actualURL, progressFn)
		case "quality":
			// Cascading quality fallback: try preferred, then step down
			chain := downloader.QualityFallbackChain(quality)
			for i, q := range chain {
				if i > 0 {
					jobLog.Warn("quality fallback", "from", chain[i-1], "to", q)
					progressFn(fmt.Sprintf("Quality %s unavailable, trying %s...", chain[i-1], q), 5)
					cleanJobDir(cfg.Downly.Worker.WorkDir, job.ID)
				}
				if q == "best" {
					res, err = dl.Download(jobCtx, cfg.Downly.Worker.WorkDir, job.ID, actualURL, progressFn)
				} else {
					res, err = dl.DownloadWithQuality(jobCtx, cfg.Downly.Worker.WorkDir, job.ID, actualURL, q, progressFn)
				}
				if err == nil || errors.Is(err, context.Canceled) || errors.Is(jobCtx.Err(), context.Canceled) {
					break
				}
			}
		default:
			res, err = dl.Download(jobCtx, cfg.Downly.Worker.WorkDir, job.ID, actualURL, progressFn)
		}
		controller.Unregister(job.ID)
		cancel()

		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(jobCtx.Err(), context.Canceled) {
				jobLog.Warn("job canceled during processing")
				_ = db.MarkCanceled(ctx, pool, job.ID)
				editProgress(ctx, b, job.ChatID, int(job.TelegramMsgID), formatCanceledMessage(job.ID))
				continue
			}
			jobLog.Error("download failed", "error", err, "retry_count", job.RetryCount)
			if job.RetryCount < cfg.Downly.Limits.MaxRetries {
				jobLog.Info("scheduling retry", "retry_count", job.RetryCount+1)
				_ = db.MarkFailedForRetry(ctx, pool, job.ID, truncate(err.Error()))
				editProgress(ctx, b, job.ChatID, int(job.TelegramMsgID), fmt.Sprintf("Job #%d failed, retrying automatically...", job.ID))
				continue
			}
			_ = db.MarkFailed(ctx, pool, job.ID, truncate(err.Error()))
			editProgress(ctx, b, job.ChatID, int(job.TelegramMsgID), formatFailureMessage(job.ID, truncate(err.Error())))
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: job.ChatID, Text: "Download failed: " + truncate(err.Error())})
			continue
		}

		fi, err := os.Stat(res.FilePath)
		if err != nil {
			jobLog.Error("stat output failed", "path", res.FilePath, "error", err)
			_ = db.MarkFailed(ctx, pool, job.ID, truncate(err.Error()))
			editProgress(ctx, b, job.ChatID, int(job.TelegramMsgID), formatFailureMessage(job.ID, truncate(err.Error())))
			continue
		}
		jobLog.Info("download completed", "platform", res.Platform, "file_name", res.FileName, "size_bytes", fi.Size(), "media_type", res.Media)

		if fi.Size() > cfg.Downly.Worker.MaxFileSizeMB*1024*1024 {
			msg := fmt.Sprintf("File too large: %.1fMB exceeds %dMB", float64(fi.Size())/1024.0/1024.0, cfg.Downly.Worker.MaxFileSizeMB)
			jobLog.Warn("file exceeds size limit", "size_bytes", fi.Size(), "max_size_mb", cfg.Downly.Worker.MaxFileSizeMB)
			_ = db.MarkFailed(ctx, pool, job.ID, truncate(msg))
			editProgress(ctx, b, job.ChatID, int(job.TelegramMsgID), formatFailureMessage(job.ID, msg))
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: job.ChatID, Text: msg})
			continue
		}

		_ = db.UpdateProgress(ctx, pool, job.ID, "Uploading to Telegram", 99)
		editProgress(ctx, b, job.ChatID, int(job.TelegramMsgID), formatProgressMessage(job.ID, job.Status, "Uploading to Telegram", 99, 0, 1, job.Priority))

		f, err := os.Open(res.FilePath)
		if err != nil {
			jobLog.Error("open output failed", "path", res.FilePath, "error", err)
			_ = db.MarkFailed(ctx, pool, job.ID, truncate(err.Error()))
			editProgress(ctx, b, job.ChatID, int(job.TelegramMsgID), formatFailureMessage(job.ID, truncate(err.Error())))
			continue
		}

		caption := buildCaption(res)
		err = sendMedia(ctx, b, job.ChatID, f, res)
		_ = f.Close()
		_ = os.Remove(res.FilePath)
		if err != nil {
			jobLog.Error("send media failed", "file_name", res.FileName, "error", err)
			_ = db.MarkFailed(ctx, pool, job.ID, truncate(err.Error()))
			editProgress(ctx, b, job.ChatID, int(job.TelegramMsgID), formatFailureMessage(job.ID, truncate(err.Error())))
			continue
		}
		_ = caption // used in sendMedia

		if err := db.MarkDone(ctx, pool, job.ID, res.FilePath, res.FileName, res.Platform); err != nil {
			jobLog.Error("mark done failed", "error", err)
			continue
		}
		editProgress(ctx, b, job.ChatID, int(job.TelegramMsgID), formatDoneMessage(job.ID, res))
		jobLog.Info("job finished", "platform", res.Platform, "file_name", res.FileName)
	}
}

func sendMedia(ctx context.Context, b *bot.Bot, chatID int64, f *os.File, res *downloader.Result) error {
	caption := buildCaption(res)
	upload := &models.InputFileUpload{Filename: res.FileName, Data: f}

	switch res.Media {
	case downloader.MediaVideo:
		_, err := b.SendVideo(ctx, &bot.SendVideoParams{
			ChatID:            chatID,
			Video:             upload,
			Caption:           caption,
			SupportsStreaming:  true,
		})
		return err
	case downloader.MediaAudio:
		_, err := b.SendAudio(ctx, &bot.SendAudioParams{
			ChatID:  chatID,
			Audio:   upload,
			Caption: caption,
			Title:   res.Title,
		})
		return err
	case downloader.MediaPhoto:
		_, err := b.SendPhoto(ctx, &bot.SendPhotoParams{
			ChatID:  chatID,
			Photo:   upload,
			Caption: caption,
		})
		return err
	default:
		_, err := b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID:   chatID,
			Document: upload,
			Caption:  caption,
		})
		return err
	}
}

func buildCaption(res *downloader.Result) string {
	parts := []string{}
	if res.Title != "" {
		title := res.Title
		if len(title) > 100 {
			title = title[:97] + "..."
		}
		parts = append(parts, title)
	}
	if res.Duration > 0 {
		m := res.Duration / 60
		s := res.Duration % 60
		parts = append(parts, fmt.Sprintf("Duration: %d:%02d", m, s))
	}
	if res.Platform != "" && res.Platform != "unknown" {
		parts = append(parts, "Source: "+res.Platform)
	}
	if len(parts) == 0 {
		return "Done."
	}
	return strings.Join(parts, "\n")
}

func editProgress(ctx context.Context, b *bot.Bot, chatID int64, messageID int, text string) {
	if messageID == 0 || text == "" {
		return
	}
	_, _ = b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: messageID, Text: text})
}

func formatProgressMessage(jobID int64, status db.JobStatus, progress string, percent, ahead, active, priority int) string {
	msg := fmt.Sprintf("Job #%d\nStatus: %s", jobID, status)
	if priority > 0 {
		msg += fmt.Sprintf("\nPriority: %d", priority)
	}
	if ahead > 0 {
		msg += fmt.Sprintf("\nQueue position: %d", ahead+1)
	}
	if active > 0 {
		msg += fmt.Sprintf("\nActive workers: %d", active)
	}
	if progress != "" {
		bar := progressBar(percent)
		msg += fmt.Sprintf("\n%s %d%%\n%s", bar, percent, progress)
	}
	return msg
}

func progressBar(percent int) string {
	total := 20
	filled := percent * total / 100
	if filled < 0 {
		filled = 0
	}
	if filled > total {
		filled = total
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", total-filled) + "]"
}

func formatFailureMessage(jobID int64, err string) string {
	return fmt.Sprintf("Job #%d\nStatus: failed\nError: %s", jobID, err)
}

func formatCanceledMessage(jobID int64) string {
	return fmt.Sprintf("Job #%d\nStatus: canceled", jobID)
}

func formatDoneMessage(jobID int64, res *downloader.Result) string {
	msg := fmt.Sprintf("Job #%d\nStatus: done", jobID)
	if res.Platform != "" && res.Platform != "unknown" {
		msg += "\nSource: " + res.Platform
	}
	if res.Title != "" {
		title := res.Title
		if len(title) > 80 {
			title = title[:77] + "..."
		}
		msg += "\n" + title
	}
	return msg
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

// cleanJobDir removes all files in a job directory for retry.
func cleanJobDir(workDir string, jobID int64) {
	jobDir := fmt.Sprintf("%s/job-%d", workDir, jobID)
	entries, err := os.ReadDir(jobDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			_ = os.Remove(fmt.Sprintf("%s/%s", jobDir, entry.Name()))
		}
	}
}

// parseJobURL extracts mode and quality from prefixed URLs.
// Formats: "audio:<url>", "q720:<url>", "q480:<url>", "q1080:<url>", "<url>"
func parseJobURL(raw string) (url, mode, quality string) {
	if strings.HasPrefix(raw, "audio:") {
		return strings.TrimPrefix(raw, "audio:"), "audio", ""
	}
	for _, q := range []string{"q360:", "q480:", "q720:", "q1080:"} {
		if strings.HasPrefix(raw, q) {
			return strings.TrimPrefix(raw, q), "quality", strings.TrimSuffix(q, ":")
		}
	}
	return raw, "default", ""
}

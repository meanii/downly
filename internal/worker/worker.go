package worker

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
)

func Loop(ctx context.Context, cfg *config.Root, pool *pgxpool.Pool, b *bot.Bot) {
	poll := time.Duration(cfg.Downly.Worker.PollIntervalSec) * time.Second
	dl := downloader.YTDLP{Bin: cfg.Downly.Services.YTDLP.Bin}

	for {
		job, err := db.ClaimJob(ctx, pool)
		if err != nil {
			time.Sleep(poll)
			continue
		}
		if job == nil {
			time.Sleep(poll)
			continue
		}

		res, err := dl.Download(ctx, cfg.Downly.Worker.WorkDir, job.ID, job.URL)
		if err != nil {
			_ = db.MarkFailed(ctx, pool, job.ID, truncate(err.Error()))
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: job.ChatID, Text: "Download failed: " + truncate(err.Error())})
			continue
		}

		fi, err := os.Stat(res.FilePath)
		if err != nil {
			_ = db.MarkFailed(ctx, pool, job.ID, truncate(err.Error()))
			continue
		}
		if fi.Size() > cfg.Downly.Worker.MaxFileSizeMB*1024*1024 {
			msg := fmt.Sprintf("File too large: %.1fMB exceeds %dMB", float64(fi.Size())/1024.0/1024.0, cfg.Downly.Worker.MaxFileSizeMB)
			_ = db.MarkFailed(ctx, pool, job.ID, truncate(msg))
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: job.ChatID, Text: msg})
			continue
		}

		f, err := os.Open(res.FilePath)
		if err != nil {
			_ = db.MarkFailed(ctx, pool, job.ID, truncate(err.Error()))
			continue
		}

		_, err = b.SendDocument(ctx, &bot.SendDocumentParams{
			ChatID: job.ChatID,
			Document: &models.InputFileUpload{
				Filename: res.FileName,
				Data:     f,
			},
			Caption: "Done. Source: " + res.Platform,
		})
		_ = f.Close()
		_ = os.Remove(res.FilePath)
		if err != nil {
			_ = db.MarkFailed(ctx, pool, job.ID, truncate(err.Error()))
			continue
		}

		_ = db.MarkDone(ctx, pool, job.ID, res.FilePath, res.FileName, res.Platform)
	}
}

func truncate(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

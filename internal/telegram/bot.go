package telegram

import (
	"context"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/db"
)

func RegisterHandlers(b *bot.Bot, pool *pgxpool.Pool) {
	b.RegisterHandler(bot.HandlerTypeMessageText, "", bot.MatchTypeContains, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message == nil || update.Message.Text == "" {
			return
		}

		text := strings.TrimSpace(update.Message.Text)
		if strings.HasPrefix(text, "/start") {
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: update.Message.Chat.ID,
				Text:   "Send me a supported media URL and I will queue it for download.",
			})
			return
		}
		if strings.HasPrefix(text, "/help") {
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID: update.Message.Chat.ID,
				Text:   "Supports many services via yt-dlp, including Instagram, YouTube, and YouTube Shorts.",
			})
			return
		}
		if strings.HasPrefix(text, "/") || !looksLikeURL(text) {
			return
		}

		reply, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: update.Message.Chat.ID,
			Text:   "Queued. I will send the file here when it is ready.",
		})
		if err != nil {
			return
		}

		userID := int64(0)
		if update.Message.From != nil {
			userID = update.Message.From.ID
		}
		_ = db.InsertJob(ctx, pool, update.Message.Chat.ID, userID, text, int64(reply.ID))
	})
}

func looksLikeURL(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

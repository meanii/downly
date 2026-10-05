package telegram

import (
	"context"
	"log/slog"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/db"
)

// updateUser extracts the acting user and, when known, their private chat ID.
func updateUser(u *models.Update) (user *models.User, privateChatID int64) {
	switch {
	case u.Message != nil:
		if u.Message.Chat.Type == models.ChatTypePrivate {
			privateChatID = u.Message.Chat.ID
		}
		return u.Message.From, privateChatID
	case u.CallbackQuery != nil:
		if m := u.CallbackQuery.Message.Message; m != nil && m.Chat.Type == models.ChatTypePrivate {
			privateChatID = m.Chat.ID
		}
		return &u.CallbackQuery.From, privateChatID
	case u.InlineQuery != nil:
		return u.InlineQuery.From, 0
	case u.ChosenInlineResult != nil:
		return &u.ChosenInlineResult.From, 0
	}
	return nil, 0
}

// UserTracker is a middleware that records every user who interacts with
// the bot, so broadcasts and /users do not depend on pruned job rows.
func UserTracker(pool *pgxpool.Pool, logger *slog.Logger) bot.Middleware {
	log := logger.With("component", "telegram", "middleware", "users")
	return func(next bot.HandlerFunc) bot.HandlerFunc {
		return func(ctx context.Context, b *bot.Bot, update *models.Update) {
			if user, chatID := updateUser(update); user != nil && !user.IsBot {
				tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
				if err := db.TouchUser(tctx, pool, user.ID, chatID, user.Username, user.FirstName); err != nil {
					log.Warn("record user failed", "user_id", user.ID, "error", err)
				}
				cancel()
			}
			next(ctx, b, update)
		}
	}
}

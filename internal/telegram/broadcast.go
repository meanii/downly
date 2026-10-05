package telegram

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/go-telegram/bot"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/tgutil"
)

// broadcastInterval keeps broadcasts under Telegram's ~30 messages/second
// global limit, leaving headroom for normal traffic.
var broadcastInterval = 50 * time.Millisecond

var broadcastRunning atomic.Bool

type broadcastResult struct {
	Sent, Blocked, Failed int
}

// runBroadcast sends to each chat at most once per interval, waiting out
// 429s. onBlocked is called for chats that have blocked the bot.
func runBroadcast(ctx context.Context, chatIDs []int64, interval time.Duration, send func(context.Context, int64) error, onBlocked func(int64)) broadcastResult {
	var res broadcastResult
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for i, id := range chatIDs {
		if i > 0 {
			select {
			case <-ctx.Done():
				res.Failed += len(chatIDs) - i
				return res
			case <-tick.C:
			}
		}
		err := tgutil.Call(ctx, 3, func() error { return send(ctx, id) })
		switch {
		case err == nil:
			res.Sent++
		case errors.Is(err, bot.ErrorForbidden):
			res.Blocked++
			if onBlocked != nil {
				onBlocked(id)
			}
		default:
			res.Failed++
		}
	}
	return res
}

func (h *handler) cmdBroadcast(ctx context.Context, r *request) {
	m, msg := r.msg, r.args
	if msg == "" {
		h.reply(ctx, m.Chat.ID, "Usage: /broadcast <message>")
		return
	}
	if !broadcastRunning.CompareAndSwap(false, true) {
		h.reply(ctx, m.Chat.ID, "A broadcast is already running. Please wait for it to finish.")
		return
	}
	chatIDs, err := db.GetAllChatIDs(ctx, h.pool)
	if err != nil {
		broadcastRunning.Store(false)
		h.log.Error("load broadcast targets failed", "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to fetch user list.")
		return
	}
	h.reply(ctx, m.Chat.ID, fmt.Sprintf("Broadcasting to %d users. I'll report back when done.", len(chatIDs)))
	h.log.Info("broadcast started", "by", m.From.ID, "targets", len(chatIDs))

	// Run in the background so the admin isn't blocked and other updates
	// keep flowing. ctx is the bot's lifetime context, so shutdown stops it.
	go func() {
		defer broadcastRunning.Store(false)
		text := truncateRunes("[Broadcast] "+msg, maxMessageLen)
		res := runBroadcast(ctx, chatIDs, broadcastInterval,
			func(ctx context.Context, id int64) error {
				_, err := h.b.SendMessage(ctx, &bot.SendMessageParams{ChatID: id, Text: text})
				return err
			},
			func(id int64) {
				if err := db.MarkUserBlocked(ctx, h.pool, id); err != nil {
					h.log.Warn("mark user blocked failed", "chat_id", id, "error", err)
				}
			},
		)
		h.log.Info("broadcast finished", "sent", res.Sent, "blocked", res.Blocked, "failed", res.Failed)
		h.reply(context.WithoutCancel(ctx), m.Chat.ID, fmt.Sprintf("Broadcast done. Sent: %d, Blocked the bot: %d, Failed: %d", res.Sent, res.Blocked, res.Failed))
	}()
}

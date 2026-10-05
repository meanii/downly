package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/i18n"
	"github.com/meanii/downly/internal/subscriptions"
)

func (h *handler) subscriptionInterval() time.Duration {
	return time.Duration(h.cfg.Downly.Subscriptions.IntervalMinutes) * time.Minute
}

func (h *handler) fetcher() subscriptions.Fetcher {
	return downloader.YTDLP{Bin: h.cfg.Downly.Services.YTDLP.Bin, CookiesFile: h.cfg.Downly.Services.YTDLP.CookiesFile, Logger: h.log}
}

// cmdFollow handles "/follow <channel or playlist url> [audio]".
func (h *handler) cmdFollow(ctx context.Context, r *request) {
	chat, lang := r.msg.Chat, r.lang
	if h.cfg.Downly.Subscriptions.Disabled {
		return
	}
	fields := strings.Fields(r.args)
	if len(fields) == 0 {
		h.reply(ctx, chat.ID, i18n.T(lang, "follow_usage"))
		return
	}
	if !h.canManage(ctx, chat, r.msg.From.ID) {
		h.reply(ctx, chat.ID, i18n.T(lang, "lang_group_admins_only"))
		return
	}
	url := normalizeURL(fields[0])
	if !isQueueableURL(url) {
		h.reply(ctx, chat.ID, i18n.T(lang, "invalid_url"))
		return
	}
	mode := db.ModeVideo
	if len(fields) > 1 && strings.EqualFold(fields[1], "audio") {
		mode = db.ModeAudio
	}
	feed := subscriptions.FeedURL(url)

	h.reply(ctx, chat.ID, i18n.T(lang, "follow_checking"))
	fctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	entries, title, err := h.fetcher().FetchPlaylist(fctx, feed, 10)
	if err != nil || len(entries) == 0 {
		h.log.Warn("follow: fetch feed failed", "url", feed, "error", err)
		h.reply(ctx, chat.ID, i18n.T(lang, "follow_failed"))
		return
	}
	if title == "" {
		title = trimURL(feed)
	}
	seen := make([]string, len(entries))
	for i, e := range entries {
		seen[i] = e.ID
	}
	_, err = db.AddSubscription(ctx, h.pool, db.Subscription{
		ChatID: chat.ID, UserID: r.msg.From.ID, URL: feed, Title: title, Mode: mode,
	}, seen, h.cfg.Downly.Subscriptions.MaxPerChat, h.subscriptionInterval())
	switch {
	case errors.Is(err, db.ErrSubscriptionExists):
		h.reply(ctx, chat.ID, i18n.T(lang, "follow_exists"))
	case errors.Is(err, db.ErrSubscriptionLimit):
		h.reply(ctx, chat.ID, i18n.T(lang, "follow_limit", h.cfg.Downly.Subscriptions.MaxPerChat))
	case err != nil:
		h.log.Error("add subscription failed", "error", err)
		h.reply(ctx, chat.ID, i18n.T(lang, "generic_error"))
	default:
		h.log.Info("subscribed", "chat_id", chat.ID, "url", feed, "mode", mode)
		h.reply(ctx, chat.ID, i18n.T(lang, "follow_ok", truncateRunes(title, 80), h.cfg.Downly.Subscriptions.IntervalMinutes))
	}
}

// followingView lists a chat's subscriptions with an unfollow button each.
func (h *handler) followingView(ctx context.Context, lang i18n.Lang, chatID int64) (string, *models.InlineKeyboardMarkup) {
	subs, err := db.ListSubscriptions(ctx, h.pool, chatID)
	if err != nil {
		h.log.Error("list subscriptions failed", "error", err)
		return i18n.T(lang, "generic_error"), nil
	}
	if len(subs) == 0 {
		return i18n.T(lang, "following_empty"), nil
	}
	lines := []string{i18n.T(lang, "following_title")}
	var rows [][]models.InlineKeyboardButton
	for i, s := range subs {
		label := s.Title
		if s.Mode == db.ModeAudio {
			label += " 🎵"
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, truncateRunes(label, 60), s.URL))
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         "❌ " + truncateRunes(s.Title, 30),
			CallbackData: fmt.Sprintf("unf:%d", s.ID),
		}})
	}
	return strings.Join(lines, "\n"), &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func (h *handler) cmdFollowing(ctx context.Context, r *request) {
	text, kb := h.followingView(ctx, r.lang, r.msg.Chat.ID)
	params := &bot.SendMessageParams{ChatID: r.msg.Chat.ID, Text: text}
	if kb != nil {
		params.ReplyMarkup = kb
	}
	if _, err := h.send(ctx, params); err != nil {
		h.log.Warn("send subscriptions failed", "error", err)
	}
}

// cmdUnfollow handles "/unfollow <number from /following>".
func (h *handler) cmdUnfollow(ctx context.Context, r *request) {
	chat, lang := r.msg.Chat, r.lang
	n, err := strconv.Atoi(strings.TrimSpace(r.args))
	if err != nil || n < 1 {
		h.reply(ctx, chat.ID, i18n.T(lang, "unfollow_usage"))
		return
	}
	if !h.canManage(ctx, chat, r.msg.From.ID) {
		h.reply(ctx, chat.ID, i18n.T(lang, "lang_group_admins_only"))
		return
	}
	subs, err := db.ListSubscriptions(ctx, h.pool, chat.ID)
	if err != nil || n > len(subs) {
		h.reply(ctx, chat.ID, i18n.T(lang, "unfollow_missing"))
		return
	}
	if ok, err := db.DeleteSubscription(ctx, h.pool, subs[n-1].ID, chat.ID); err != nil || !ok {
		h.reply(ctx, chat.ID, i18n.T(lang, "unfollow_missing"))
		return
	}
	h.reply(ctx, chat.ID, i18n.T(lang, "unfollow_ok", truncateRunes(subs[n-1].Title, 80)))
}

// onUnfollowCallback handles "unf:<subscription id>" buttons.
func (h *handler) onUnfollowCallback(ctx context.Context, _ *bot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	m := cb.Message.Message
	if m == nil {
		return
	}
	lang := h.langFor(ctx, m.Chat.ID, &cb.From)
	id, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "unf:"), 10, 64)
	if err != nil {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "button_invalid"))
		return
	}
	if !h.canManage(ctx, m.Chat, cb.From.ID) {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "lang_group_admins_only"))
		return
	}
	// Scoped to this chat: an ID from another chat's list deletes nothing.
	if ok, err := db.DeleteSubscription(ctx, h.pool, id, m.Chat.ID); err != nil || !ok {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "unfollow_missing"))
		return
	}
	h.answerCallback(ctx, cb.ID, i18n.T(lang, "unfollow_toast"))
	text, kb := h.followingView(ctx, lang, m.Chat.ID)
	h.editMarkup(ctx, m, text, kb)
}

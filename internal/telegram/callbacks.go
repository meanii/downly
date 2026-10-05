package telegram

import (
	"context"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/i18n"
	"github.com/meanii/downly/internal/media"
	"github.com/meanii/downly/internal/safeurl"
	"github.com/meanii/downly/internal/worker"
)

// callbackChat returns the chat a callback button lives in, falling back to
// the user's private chat.
func callbackChat(cb *models.CallbackQuery) int64 {
	if m := cb.Message.Message; m != nil {
		return m.Chat.ID
	}
	return cb.From.ID
}

// onQualityCallback handles "dl:<quality>:<token>" from /quality buttons.
func (h *handler) onQualityCallback(ctx context.Context, _ *bot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	userID, chatID := cb.From.ID, callbackChat(cb)
	lang := h.langFor(ctx, chatID, &cb.From)

	data := strings.TrimPrefix(cb.Data, "dl:")
	quality, token, found := strings.Cut(data, ":")
	if !found {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "button_invalid"))
		return
	}
	// Callback data comes from the client and can be forged; trust nothing.
	if !downloader.ValidQuality(quality) {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "quality_unknown"))
		return
	}
	url, ok := pendingURLs.Get(token)
	if !ok {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "button_expired"))
		return
	}
	if _, err := safeurl.Validate(url); err != nil {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "invalid_url"))
		return
	}
	label := quality
	if quality == "qbest" || quality == "best" {
		label = i18n.T(lang, "quality_best")
	}
	h.answerCallback(ctx, cb.ID, i18n.T(lang, "downloading_at", label))

	if !h.allowSubmit(ctx, chatID, userID, lang) {
		return
	}
	queued := url
	if quality != "qbest" && quality != "best" {
		queued = quality + ":" + url
	}
	_, _ = h.enqueue(ctx, download{chatID: chatID, userID: userID, lang: lang, url: queued})
}

// onSetQualityCallback handles "sq:<value>[:s]" from quality preference
// buttons; the ":s" suffix means it was opened from the settings menu.
func (h *handler) onSetQualityCallback(ctx context.Context, _ *bot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	lang := h.langFor(ctx, callbackChat(cb), &cb.From)
	quality, origin, _ := strings.Cut(strings.TrimPrefix(cb.Data, "sq:"), ":")
	if !isPreferenceValue(quality) {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "quality_unknown"))
		return
	}
	if err := db.SetUserQuality(ctx, h.pool, cb.From.ID, quality); err != nil {
		h.log.Error("save quality preference failed", "user_id", cb.From.ID, "error", err)
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "quality_save_failed"))
		return
	}
	label := qualityLabel(lang, quality)
	h.answerCallback(ctx, cb.ID, i18n.T(lang, "quality_set_toast", label))

	m := cb.Message.Message
	if m == nil {
		return
	}
	if origin == originSettings {
		text, kb := h.settingsView(ctx, lang, m.Chat, cb.From.ID)
		h.editMarkup(ctx, m, text, kb)
		return
	}
	h.editMarkup(ctx, m, i18n.T(lang, "quality_saved", label), qualityPreferenceKeyboard(lang, quality, false))
}

// Inline result variants: ID suffix, title key and the job prefix queued.
var inlineResults = []struct {
	ID, TitleKey, Prefix string
}{
	{"best", "inline_title_best", ""},
	{"720", "inline_title_720", "q720:"},
	{"480", "inline_title_480", "q480:"},
	{"mp3", "inline_title_audio", "audio:"},
}

// Inline result IDs are "dl_<variant>" for a fresh download and
// "c_<variant>" for media served from the cache.
const (
	inlineDownloadPrefix = "dl_"
	inlineCachedPrefix   = "c_"
)

func inlineVariant(resultID string) (prefix string, cached, ok bool) {
	id, cached := strings.CutPrefix(resultID, inlineCachedPrefix)
	if !cached {
		var found bool
		if id, found = strings.CutPrefix(resultID, inlineDownloadPrefix); !found {
			return "", false, false
		}
	}
	for _, r := range inlineResults {
		if r.ID == id {
			return r.Prefix, cached, true
		}
	}
	return "", false, false
}

// cacheKeyFor is the media cache key a prefixed URL would be stored under.
func cacheKeyFor(prefixedURL string) string {
	u, mode, quality := jobSpec(prefixedURL)
	return media.CacheKey(u, string(mode), quality, "")
}

// cachedInlineResult turns a single-file cache entry into an inline result
// that shares the media itself.
func cachedInlineResult(id, title string, e *db.CacheEntry, caption string) models.InlineQueryResult {
	it := e.Items[0]
	switch it.Kind {
	case media.Video:
		return &models.InlineQueryResultCachedVideo{ID: id, VideoFileID: it.FileID, Title: title, Caption: caption}
	case media.Audio:
		return &models.InlineQueryResultCachedAudio{ID: id, AudioFileID: it.FileID, Caption: caption}
	case media.Photo:
		return &models.InlineQueryResultCachedPhoto{ID: id, PhotoFileID: it.FileID, Title: title, Caption: caption}
	default:
		return &models.InlineQueryResultCachedDocument{ID: id, DocumentFileID: it.FileID, Title: title, Caption: caption}
	}
}

func (h *handler) onInlineQuery(ctx context.Context, _ *bot.Bot, update *models.Update) {
	iq := update.InlineQuery
	// Inline queries have no chat; use the user's own (private chat) setting.
	lang := h.langFor(ctx, iq.From.ID, iq.From)
	botUser := getBotUsername(ctx, h.b)
	query := strings.TrimSpace(iq.Query)

	var results []models.InlineQueryResult
	url := normalizeURL(query)
	switch {
	case query == "" || !looksLikeURL(url):
		results = []models.InlineQueryResult{&models.InlineQueryResultArticle{
			ID:          "help",
			Title:       i18n.T(lang, "inline_help_title"),
			Description: i18n.T(lang, "inline_help_desc"),
			InputMessageContent: &models.InputTextMessageContent{
				MessageText: i18n.T(lang, "inline_help_text", botUser),
			},
		}}
	case !isQueueableURL(url):
		return
	default:
		if banned, err := db.IsBanned(ctx, h.pool, iq.From.ID); err != nil {
			h.log.Error("ban check failed", "user_id", iq.From.ID, "error", err)
		} else if banned {
			return
		}
		short := trimURL(url)
		// Cached variants share the media itself, instantly; list them first.
		var fresh []models.InlineQueryResult
		for _, r := range inlineResults {
			title := i18n.T(lang, r.TitleKey)
			if !h.cfg.Downly.Cache.Disabled {
				if e, ok, err := db.GetCache(ctx, h.pool, cacheKeyFor(r.Prefix+url)); err == nil && ok && len(e.Items) == 1 {
					results = append(results, cachedInlineResult(inlineCachedPrefix+r.ID, "⚡ "+title, e, worker.Caption(lang, e.Meta)))
					continue
				}
			}
			// The placeholder button makes Telegram report an inline message ID,
			// so the worker can swap the text for the media once it's ready.
			fresh = append(fresh, &models.InlineQueryResultArticle{
				ID:          inlineDownloadPrefix + r.ID,
				Title:       title,
				Description: short,
				InputMessageContent: &models.InputTextMessageContent{
					MessageText: i18n.T(lang, "inline_msg", short, botUser),
				},
				ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
					{Text: "⏳", CallbackData: "noop"},
				}}},
			})
		}
		results = append(results, fresh...)
	}
	if _, err := h.b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
		InlineQueryID: iq.ID,
		Results:       results,
		CacheTime:     5,
		IsPersonal:    true,
	}); err != nil {
		h.log.Warn("answer inline query failed", "error", err)
	}
}

// onChosenInlineResult handles a picked inline result. Cached results were
// already shared and are only recorded; fresh ones are queued, delivered to
// the user's private chat (so they must have started the bot) and then
// swapped into the shared message. Needs inline feedback enabled in
// @BotFather (/setinlinefeedback).
func (h *handler) onChosenInlineResult(ctx context.Context, _ *bot.Bot, update *models.Update) {
	chosen := update.ChosenInlineResult
	userID := chosen.From.ID
	chatID := userID
	lang := h.langFor(ctx, chatID, &chosen.From)
	url := normalizeURL(strings.TrimSpace(chosen.Query))
	prefix, cached, ok := inlineVariant(chosen.ResultID)
	if !ok || !looksLikeURL(url) || !isQueueableURL(prefix+url) {
		return
	}
	if cached {
		h.recordInlineCached(ctx, userID, prefix+url)
		return
	}
	if !h.allowSubmit(ctx, chatID, userID, lang) {
		return
	}
	_, _ = h.enqueue(ctx, download{chatID: chatID, userID: userID, lang: lang, url: prefix + url, inlineMessageID: chosen.InlineMessageID})
}

// recordInlineCached logs an inline share of cached media as a finished job.
func (h *handler) recordInlineCached(ctx context.Context, userID int64, prefixedURL string) {
	e, ok, err := db.GetCache(ctx, h.pool, cacheKeyFor(prefixedURL))
	if err != nil || !ok {
		return
	}
	u, mode, quality := jobSpec(prefixedURL)
	if _, err := db.InsertCachedJob(ctx, h.pool, db.NewJob{ChatID: userID, UserID: userID, URL: u, Mode: mode, Quality: quality}, e); err != nil {
		h.log.Warn("record inline share failed", "error", err)
	}
	_ = db.TouchCache(ctx, h.pool, e.Key)
}

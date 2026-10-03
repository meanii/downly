package telegram

import (
	"context"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/i18n"
	"github.com/meanii/downly/internal/safeurl"
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
	_, _ = h.enqueue(ctx, chatID, userID, lang, queued)
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

// Inline result IDs, their title keys and the job prefix each one queues.
var inlineResults = []struct {
	ID, TitleKey, Prefix string
}{
	{"dl_best", "inline_title_best", ""},
	{"dl_720", "inline_title_720", "q720:"},
	{"dl_480", "inline_title_480", "q480:"},
	{"dl_mp3", "inline_title_audio", "audio:"},
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
		for _, r := range inlineResults {
			results = append(results, &models.InlineQueryResultArticle{
				ID:          r.ID,
				Title:       i18n.T(lang, r.TitleKey),
				Description: short,
				InputMessageContent: &models.InputTextMessageContent{
					MessageText: i18n.T(lang, "inline_msg", short, botUser),
				},
			})
		}
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

// onChosenInlineResult queues the download picked from inline mode. It needs
// inline feedback enabled in @BotFather (/setinlinefeedback), and the file is
// delivered to the user's private chat, so they must have started the bot.
func (h *handler) onChosenInlineResult(ctx context.Context, _ *bot.Bot, update *models.Update) {
	chosen := update.ChosenInlineResult
	userID := chosen.From.ID
	chatID := userID // deliver download to user's DM
	lang := h.langFor(ctx, chatID, &chosen.From)
	url := normalizeURL(strings.TrimSpace(chosen.Query))
	if !looksLikeURL(url) {
		return
	}
	prefix := ""
	for _, r := range inlineResults {
		if r.ID == chosen.ResultID {
			prefix = r.Prefix
		}
	}
	if !h.allowSubmit(ctx, chatID, userID, lang) {
		return
	}
	_, _ = h.enqueue(ctx, chatID, userID, lang, prefix+url)
}

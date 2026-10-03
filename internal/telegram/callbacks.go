package telegram

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
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
	data := strings.TrimPrefix(cb.Data, "dl:")
	quality, token, found := strings.Cut(data, ":")
	if !found {
		h.answerCallback(ctx, cb.ID, "Invalid button.")
		return
	}
	// Callback data comes from the client and can be forged; trust nothing.
	if !downloader.ValidQuality(quality) {
		h.answerCallback(ctx, cb.ID, "Unknown quality.")
		return
	}
	url, ok := pendingURLs.Get(token)
	if !ok {
		h.answerCallback(ctx, cb.ID, "This button has expired. Send the link again.")
		return
	}
	if _, err := safeurl.Validate(url); err != nil {
		h.answerCallback(ctx, cb.ID, "Invalid URL.")
		return
	}
	h.answerCallback(ctx, cb.ID, "Downloading at "+quality+"...")

	userID, chatID := cb.From.ID, callbackChat(cb)
	if !h.allowSubmit(ctx, chatID, userID) {
		return
	}
	queued := url
	if quality != "qbest" && quality != "best" {
		queued = quality + ":" + url
	}
	_, _ = h.enqueue(ctx, chatID, userID, queued)
}

// onSetQualityCallback handles "sq:<value>" from /setquality buttons.
func (h *handler) onSetQualityCallback(ctx context.Context, _ *bot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	quality := strings.TrimPrefix(cb.Data, "sq:")
	if !isPreferenceValue(quality) {
		h.answerCallback(ctx, cb.ID, "Unknown quality.")
		return
	}
	if err := db.SetUserQuality(ctx, h.pool, cb.From.ID, quality); err != nil {
		h.log.Error("save quality preference failed", "user_id", cb.From.ID, "error", err)
		h.answerCallback(ctx, cb.ID, "Failed to save preference.")
		return
	}
	label := qualityLabel(quality)
	h.answerCallback(ctx, cb.ID, "Quality set to "+label)

	if m := cb.Message.Message; m != nil {
		_, err := h.b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:      m.Chat.ID,
			MessageID:   m.ID,
			Text:        fmt.Sprintf("Quality preference saved: %s\nAll your downloads will now use this setting. If unavailable, it falls back to lower resolutions automatically.", label),
			ReplyMarkup: qualityPreferenceKeyboard(quality),
		})
		if err != nil && !strings.Contains(err.Error(), "message is not modified") {
			h.log.Warn("edit quality picker failed", "error", err)
		}
	}
}

// Inline result IDs and the job prefix each one queues.
var inlineResults = []struct {
	ID, Title, Label, Prefix string
}{
	{"dl_best", "Download (best quality)", "best quality", ""},
	{"dl_720", "Download (720p)", "720p", "q720:"},
	{"dl_480", "Download (480p)", "480p", "q480:"},
	{"dl_mp3", "Download (audio only)", "audio", "audio:"},
}

func (h *handler) onInlineQuery(ctx context.Context, _ *bot.Bot, update *models.Update) {
	iq := update.InlineQuery
	botUser := getBotUsername(ctx, h.b)
	query := strings.TrimSpace(iq.Query)

	var results []models.InlineQueryResult
	url := normalizeURL(query)
	switch {
	case query == "" || !looksLikeURL(url):
		results = []models.InlineQueryResult{&models.InlineQueryResultArticle{
			ID:          "help",
			Title:       "Downly - Media Downloader",
			Description: "Paste a URL to queue a download",
			InputMessageContent: &models.InputTextMessageContent{
				MessageText: "Send a media URL to @" + botUser + " to download it.",
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
				Title:       r.Title,
				Description: short,
				InputMessageContent: &models.InputTextMessageContent{
					MessageText: fmt.Sprintf("Downloading %s via @%s (%s)", short, botUser, r.Label),
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
	if !h.allowSubmit(ctx, chatID, userID) {
		return
	}
	_, _ = h.enqueue(ctx, chatID, userID, prefix+url)
}

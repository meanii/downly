package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/i18n"
)

// maxAgainButtons caps the "Send again" buttons under /history.
const maxAgainButtons = 5

// againKeyboard offers to resend the most recent finished downloads.
func againKeyboard(lang i18n.Lang, jobs []db.Job) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	for _, j := range jobs {
		if j.Status != db.StatusDone {
			continue
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         i18n.T(lang, "btn_again", j.ID),
			CallbackData: fmt.Sprintf("again:%d", j.ID),
		}})
		if len(rows) == maxAgainButtons {
			break
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

// onAgainCallback resends a past download: instantly from the cache when
// possible, otherwise by queueing it again.
func (h *handler) onAgainCallback(ctx context.Context, _ *bot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	chatID := callbackChat(cb)
	lang := h.langFor(ctx, chatID, &cb.From)
	id, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "again:"), 10, 64)
	if err != nil {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "button_invalid"))
		return
	}
	// Only the owner may resend; the ID in callback data is client-controlled.
	job, err := db.GetUserJob(ctx, h.pool, id, cb.From.ID)
	if err != nil || job == nil {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "button_invalid"))
		return
	}
	h.answerCallback(ctx, cb.ID, i18n.T(lang, "again_sending"))

	d := download{chatID: chatID, userID: cb.From.ID, lang: lang, url: jobPrefix(job) + job.URL}
	if !h.allowSubmit(ctx, chatID, cb.From.ID, lang) {
		return
	}
	_, _ = h.enqueue(ctx, d)
}

// onNoopCallback answers the placeholder button on pending inline messages.
func (h *handler) onNoopCallback(ctx context.Context, _ *bot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	h.answerCallback(ctx, cb.ID, i18n.T(h.langFor(ctx, cb.From.ID, &cb.From), "inline_wait"))
}

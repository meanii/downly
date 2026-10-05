package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/i18n"
)

// starsCurrency is Telegram Stars; digital goods must be sold in it.
const starsCurrency = "XTR"

func (h *handler) premiumEnabled() bool { return h.cfg.Downly.Premium.Enabled }

// isPremium reports whether userID currently has premium (always false
// when premium is disabled).
func (h *handler) isPremium(ctx context.Context, userID int64) bool {
	if !h.premiumEnabled() {
		return false
	}
	ok, err := db.IsPremium(ctx, h.pool, userID)
	if err != nil {
		h.log.Warn("premium lookup failed", "user_id", userID, "error", err)
	}
	return ok
}

// premiumPayload encodes what is being bought, so the pre-checkout and
// payment handlers can verify it matches the current offer.
func premiumPayload(userID int64, days, amount int) string {
	return fmt.Sprintf("premium:%d:%d:%d", userID, days, amount)
}

func parsePremiumPayload(p string) (userID int64, days, amount int, ok bool) {
	parts := strings.Split(p, ":")
	if len(parts) != 4 || parts[0] != "premium" {
		return 0, 0, 0, false
	}
	uid, err1 := strconv.ParseInt(parts[1], 10, 64)
	d, err2 := strconv.Atoi(parts[2])
	a, err3 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || d <= 0 || a <= 0 {
		return 0, 0, 0, false
	}
	return uid, d, a, true
}

func (h *handler) cmdPremium(ctx context.Context, r *request) {
	lang, chatID := r.lang, r.msg.Chat.ID
	if !h.premiumEnabled() {
		h.reply(ctx, chatID, i18n.T(lang, "premium_disabled"))
		return
	}
	cfg := h.cfg.Downly.Premium
	status := i18n.T(lang, "premium_status_inactive")
	if until, ok, err := db.PremiumUntil(ctx, h.pool, r.msg.From.ID); err == nil && ok && until.After(time.Now()) {
		status = i18n.T(lang, "premium_status_active", until.UTC().Format("2006-01-02"))
	}
	text := status + "\n\n" + i18n.T(lang, "premium_benefits", cfg.MaxQueued)
	kb := &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{{
		{Text: i18n.T(lang, "btn_buy_premium", cfg.PriceStars, cfg.Days), CallbackData: "buy:premium"},
	}}}
	if _, err := h.send(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text, ReplyMarkup: kb}); err != nil {
		h.log.Warn("send premium info failed", "error", err)
	}
}

// onBuyCallback sends a Stars invoice to the buyer's private chat.
func (h *handler) onBuyCallback(ctx context.Context, _ *bot.Bot, update *models.Update) {
	cb := update.CallbackQuery
	lang := h.langFor(ctx, cb.From.ID, &cb.From)
	if !h.premiumEnabled() {
		h.answerCallback(ctx, cb.ID, i18n.T(lang, "premium_disabled"))
		return
	}
	h.answerCallback(ctx, cb.ID, "")
	cfg := h.cfg.Downly.Premium
	_, err := h.b.SendInvoice(ctx, &bot.SendInvoiceParams{
		ChatID:      cb.From.ID,
		Title:       i18n.T(lang, "invoice_title"),
		Description: i18n.T(lang, "invoice_desc", cfg.Days),
		Payload:     premiumPayload(cb.From.ID, cfg.Days, cfg.PriceStars),
		Currency:    starsCurrency,
		Prices:      []models.LabeledPrice{{Label: i18n.T(lang, "invoice_title"), Amount: cfg.PriceStars}},
	})
	if err != nil {
		h.log.Error("send invoice failed", "user_id", cb.From.ID, "error", err)
		h.reply(ctx, cb.From.ID, i18n.T(lang, "generic_error"))
	}
}

// onPreCheckout approves a payment only if it matches the current offer and
// is made by the user it was issued to. Telegram requires an answer within
// 10 seconds.
func (h *handler) onPreCheckout(ctx context.Context, _ *bot.Bot, update *models.Update) {
	q := update.PreCheckoutQuery
	lang := i18n.Default
	if q.From != nil {
		lang = h.langFor(ctx, q.From.ID, q.From)
	}
	cfg := h.cfg.Downly.Premium
	uid, days, amount, ok := parsePremiumPayload(q.InvoicePayload)
	valid := h.premiumEnabled() && ok && q.From != nil && uid == q.From.ID &&
		q.Currency == starsCurrency && amount == cfg.PriceStars && q.TotalAmount == cfg.PriceStars && days == cfg.Days
	params := &bot.AnswerPreCheckoutQueryParams{PreCheckoutQueryID: q.ID, OK: valid}
	if !valid {
		params.ErrorMessage = i18n.T(lang, "precheckout_invalid")
		h.log.Warn("rejected pre-checkout", "payload", q.InvoicePayload, "currency", q.Currency, "amount", q.TotalAmount)
	}
	if _, err := h.b.AnswerPreCheckoutQuery(ctx, params); err != nil {
		h.log.Error("answer pre-checkout failed", "error", err)
	}
}

// onSuccessfulPayment grants premium. Recording is idempotent on the charge
// ID, so a redelivered update can't extend premium twice.
func (h *handler) onSuccessfulPayment(ctx context.Context, _ *bot.Bot, update *models.Update) {
	msg := update.Message
	sp := msg.SuccessfulPayment
	if msg.From == nil {
		return
	}
	lang := h.langFor(ctx, msg.Chat.ID, msg.From)
	uid, days, _, ok := parsePremiumPayload(sp.InvoicePayload)
	if !ok || uid != msg.From.ID {
		h.log.Error("payment with unexpected payload", "payload", sp.InvoicePayload, "charge_id", sp.TelegramPaymentChargeID)
		return
	}
	until, applied, err := db.RecordPremiumPayment(ctx, h.pool, db.Payment{
		ChargeID: sp.TelegramPaymentChargeID, UserID: uid, Amount: sp.TotalAmount,
		Currency: sp.Currency, Payload: sp.InvoicePayload, Days: days,
	})
	if err != nil {
		// The money was taken; this must be visible to an operator.
		h.log.Error("record payment failed", "charge_id", sp.TelegramPaymentChargeID, "user_id", uid, "error", err)
		h.reply(ctx, msg.Chat.ID, i18n.T(lang, "generic_error"))
		return
	}
	if !applied {
		return
	}
	h.log.Info("premium purchased", "user_id", uid, "days", days, "amount", sp.TotalAmount, "until", until)
	h.reply(ctx, msg.Chat.ID, i18n.T(lang, "premium_thanks", until.UTC().Format("2006-01-02")))
}

func (h *handler) cmdPaySupport(ctx context.Context, r *request) {
	h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "paysupport"))
}

// cmdRefund is admin-only: /refund <charge_id>.
func (h *handler) cmdRefund(ctx context.Context, r *request) {
	chargeID := strings.TrimSpace(r.args)
	if chargeID == "" {
		h.reply(ctx, r.msg.Chat.ID, "Usage: /refund <telegram_payment_charge_id>")
		return
	}
	p, refundable, err := db.GetPayment(ctx, h.pool, chargeID)
	switch {
	case err != nil:
		h.reply(ctx, r.msg.Chat.ID, "Failed to load payment.")
		return
	case p == nil:
		h.reply(ctx, r.msg.Chat.ID, "No such payment.")
		return
	case !refundable:
		h.reply(ctx, r.msg.Chat.ID, "Already refunded.")
		return
	}
	if _, err := h.b.RefundStarPayment(ctx, &bot.RefundStarPaymentParams{UserID: p.UserID, TelegramPaymentChargeID: chargeID}); err != nil {
		h.log.Error("refund failed", "charge_id", chargeID, "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Refund failed: "+err.Error())
		return
	}
	if err := db.MarkRefunded(ctx, h.pool, chargeID); err != nil {
		h.log.Error("mark refunded failed", "charge_id", chargeID, "error", err)
	}
	h.log.Info("payment refunded", "charge_id", chargeID, "user_id", p.UserID, "by", r.msg.From.ID)
	h.reply(ctx, r.msg.Chat.ID, fmt.Sprintf("Refunded %d Stars to user %d; their premium was shortened by %d days.", p.Amount, p.UserID, p.Days))
}

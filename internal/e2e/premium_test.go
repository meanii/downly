package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
)

const premiumAdmin = 899

func withPremium(c *config.Root) {
	c.Downly.Premium = config.Premium{Enabled: true, PriceStars: 100, Days: 30, MaxQueued: 20}
	c.Downly.Limits.DailyQuotaPerUser = 1
	c.Downly.Admin.UserIDs = []int64{premiumAdmin}
}

func (e *env) preCheckout(userID int64, amount int, payload string) tgtestAnswer {
	updateID++
	e.b.ProcessUpdate(context.Background(), &models.Update{
		ID: updateID,
		PreCheckoutQuery: &models.PreCheckoutQuery{
			ID: fmt.Sprint(updateID), From: &models.User{ID: userID}, Currency: "XTR", TotalAmount: amount, InvoicePayload: payload,
		},
	})
	calls := e.api.CallsTo("answerPreCheckoutQuery")
	c := calls[len(calls)-1]
	return tgtestAnswer{ok: c.Fields["ok"] == "true", errMsg: c.Fields["error_message"]}
}

type tgtestAnswer struct {
	ok     bool
	errMsg string
}

func (e *env) pay(userID int64, chargeID, payload string) {
	updateID++
	e.b.ProcessUpdate(context.Background(), &models.Update{
		ID: updateID,
		Message: &models.Message{
			ID: int(updateID), From: &models.User{ID: userID}, Chat: private(userID),
			SuccessfulPayment: &models.SuccessfulPayment{
				Currency: "XTR", TotalAmount: 100, InvoicePayload: payload, TelegramPaymentChargeID: chargeID,
			},
		},
	})
}

func (e *env) premiumDays(userID int64) float64 {
	until, ok, err := db.PremiumUntil(context.Background(), e.pool, userID)
	if err != nil || !ok {
		return 0
	}
	return time.Until(until).Hours() / 24
}

func TestE2EPremiumPurchaseFlow(t *testing.T) {
	e := newEnv(t, withPremium)
	const user = 801

	e.send(private(user), user, "/premium")
	if !strings.Contains(e.api.LastText(user), "don't have Premium") || !strings.Contains(e.api.LastMarkup(user), `"buy:premium"`) {
		t.Fatalf("premium page = %q %s", e.api.LastText(user), e.api.LastMarkup(user))
	}

	e.press(private(user), user, "buy:premium")
	inv := e.api.CallsTo("sendInvoice")
	if len(inv) != 1 {
		t.Fatal("no invoice sent")
	}
	f := inv[0].Fields
	payload := fmt.Sprintf("premium:%d:30:100", user)
	if f["currency"] != "XTR" || f["payload"] != payload || !strings.Contains(f["prices"], `"amount":100`) || f["chat_id"] != fmt.Sprint(user) {
		t.Fatalf("invoice = %+v", f)
	}

	// Checkout is approved only for the exact offer and the right buyer.
	if a := e.preCheckout(user, 100, payload); !a.ok {
		t.Fatalf("valid checkout rejected: %+v", a)
	}
	if a := e.preCheckout(user, 50, payload); a.ok || a.errMsg == "" {
		t.Fatal("wrong amount approved")
	}
	if a := e.preCheckout(802, 100, payload); a.ok {
		t.Fatal("someone else's invoice approved")
	}
	if a := e.preCheckout(user, 100, "premium:801:3650:100"); a.ok {
		t.Fatal("tampered duration approved")
	}

	e.pay(user, "charge-1", payload)
	if d := e.premiumDays(user); d < 29.9 || d > 30.1 {
		t.Fatalf("premium days = %.2f", d)
	}
	if !strings.Contains(e.api.LastText(user), "Thank you") {
		t.Fatalf("thanks = %q", e.api.LastText(user))
	}

	// Telegram may redeliver the update: no double credit.
	e.pay(user, "charge-1", payload)
	if d := e.premiumDays(user); d > 30.1 {
		t.Fatalf("duplicate payment extended premium: %.2f days", d)
	}
	// A second purchase stacks.
	e.pay(user, "charge-2", payload)
	if d := e.premiumDays(user); d < 59.9 || d > 60.1 {
		t.Fatalf("stacked premium days = %.2f", d)
	}

	e.send(private(user), user, "/premium")
	if !strings.Contains(e.api.LastText(user), "Premium is active until") {
		t.Fatalf("status = %q", e.api.LastText(user))
	}
}

func TestE2EPremiumLimitsAndPriority(t *testing.T) {
	e := newEnv(t, withPremium)
	const free, paid = 811, 812
	e.pay(paid, "charge-p", fmt.Sprintf("premium:%d:30:100", paid))

	e.send(private(free), free, "https://1.1.1.1/watch?v=a1 https://1.1.1.1/watch?v=a2")
	if !e.api.AnyText(free, "Daily limit reached") {
		t.Fatal("free user should hit the daily quota of 1")
	}
	e.send(private(paid), paid, "https://1.1.1.1/watch?v=b1 https://1.1.1.1/watch?v=b2")
	if e.api.AnyText(paid, "Daily limit") {
		t.Fatal("premium user was limited")
	}
	jobs, _ := db.GetUserJobs(context.Background(), e.pool, paid, 10)
	if len(jobs) != 2 || jobs[0].Priority != 1 || jobs[1].Priority != 1 {
		t.Fatalf("premium jobs = %+v", jobs)
	}
}

func TestE2EPremiumRefundAndStats(t *testing.T) {
	e := newEnv(t, withPremium)
	const user = 821
	payload := fmt.Sprintf("premium:%d:30:100", user)
	e.pay(user, "charge-r", payload)

	e.send(private(premiumAdmin), premiumAdmin, "/stats")
	stats := e.api.LastText(premiumAdmin)
	if !strings.Contains(stats, "Users (7d):") || !strings.Contains(stats, "Premium: 1 active, 1 payments, 100 Stars revenue") {
		t.Fatalf("stats = %q", stats)
	}

	e.send(private(user), user, "/refund charge-r")
	if len(e.api.CallsTo("refundStarPayment")) != 0 {
		t.Fatal("non-admin triggered a refund")
	}
	e.send(private(premiumAdmin), premiumAdmin, "/refund charge-r")
	refunds := e.api.CallsTo("refundStarPayment")
	if len(refunds) != 1 || refunds[0].Fields["telegram_payment_charge_id"] != "charge-r" || refunds[0].Fields["user_id"] != fmt.Sprint(user) {
		t.Fatalf("refund calls = %+v", refunds)
	}
	if d := e.premiumDays(user); d > 0.1 {
		t.Fatalf("premium not revoked after refund: %.2f days left", d)
	}
	e.send(private(premiumAdmin), premiumAdmin, "/refund charge-r")
	if !strings.Contains(e.api.LastText(premiumAdmin), "Already refunded") {
		t.Fatalf("second refund = %q", e.api.LastText(premiumAdmin))
	}
}

func TestE2EPremiumDisabled(t *testing.T) {
	e := newEnv(t)
	const user = 831
	e.send(private(user), user, "/premium")
	if !strings.Contains(e.api.LastText(user), "isn't available") {
		t.Fatalf("got %q", e.api.LastText(user))
	}
	if a := e.preCheckout(user, 100, fmt.Sprintf("premium:%d:30:100", user)); a.ok {
		t.Fatal("checkout approved while premium is disabled")
	}
	e.send(private(user), user, "/paysupport")
	if !strings.Contains(e.api.LastText(user), "help with a payment") {
		t.Fatalf("paysupport = %q", e.api.LastText(user))
	}
}

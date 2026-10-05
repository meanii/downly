package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/dbtest"
	"github.com/meanii/downly/internal/tgtest"
	"github.com/meanii/downly/internal/worker"
)

type botHarness struct {
	t    *testing.T
	tg   *tgtest.Server
	b    *bot.Bot
	pool *pgxpool.Pool
	cfg  *config.Root
}

const adminID = 999

func newBotHarness(t *testing.T) *botHarness {
	t.Helper()
	pool := dbtest.NewPool(t)
	tg := tgtest.New(t)

	cfg := &config.Root{}
	cfg.Downly.Limits.MaxQueuedPerUser = 5
	cfg.Downly.Limits.RateLimitSeconds = 0
	cfg.Downly.Admin.UserIDs = []int64{adminID}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	b, err := bot.New("123:TEST", bot.WithServerURL(tg.URL()), bot.WithNotAsyncHandlers(),
		bot.WithMiddlewares(UserTracker(pool, logger)))
	if err != nil {
		t.Fatal(err)
	}
	RegisterHandlers(logger, cfg, worker.NewController(), b, pool)
	return &botHarness{t: t, tg: tg, b: b, pool: pool, cfg: cfg}
}

var nextUpdateID int64

func (h *botHarness) message(userID int64, text string) {
	nextUpdateID++
	h.b.ProcessUpdate(context.Background(), &models.Update{
		ID: nextUpdateID,
		Message: &models.Message{
			ID:   int(nextUpdateID),
			From: &models.User{ID: userID, FirstName: "U"},
			Chat: models.Chat{ID: userID, Type: models.ChatTypePrivate},
			Text: text,
		},
	})
}

func (h *botHarness) callback(userID int64, data string) {
	nextUpdateID++
	h.b.ProcessUpdate(context.Background(), &models.Update{
		ID: nextUpdateID,
		CallbackQuery: &models.CallbackQuery{
			ID:   fmt.Sprint(nextUpdateID),
			From: models.User{ID: userID},
			Data: data,
		},
	})
}

// groupMessage sends text from userID in group chatID.
func (h *botHarness) groupMessage(chatID, userID int64, text string) {
	nextUpdateID++
	h.b.ProcessUpdate(context.Background(), &models.Update{
		ID: nextUpdateID,
		Message: &models.Message{
			ID:   int(nextUpdateID),
			From: &models.User{ID: userID, FirstName: "U"},
			Chat: models.Chat{ID: chatID, Type: models.ChatTypeSupergroup},
			Text: text,
		},
	})
}

// buttonPress presses a button on a message in chat.
func (h *botHarness) buttonPress(chat models.Chat, userID int64, data string) {
	nextUpdateID++
	h.b.ProcessUpdate(context.Background(), &models.Update{
		ID: nextUpdateID,
		CallbackQuery: &models.CallbackQuery{
			ID:      fmt.Sprint(nextUpdateID),
			From:    models.User{ID: userID},
			Message: models.MaybeInaccessibleMessage{Message: &models.Message{ID: 42, Chat: chat}},
			Data:    data,
		},
	})
}

func (h *botHarness) jobs(userID int64) []db.Job {
	h.t.Helper()
	jobs, err := db.GetUserJobs(context.Background(), h.pool, userID, 100)
	if err != nil {
		h.t.Fatal(err)
	}
	return jobs
}

func TestHandlerQueuesURL(t *testing.T) {
	h := newBotHarness(t)
	h.message(1, "check this https://www.youtube.com/watch?v=abc and q720:https://youtu.be/xyz")
	jobs := h.jobs(1)
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want 2", len(jobs))
	}
	// newest first
	if jobs[0].Quality != "q720" || jobs[0].URL != "https://youtu.be/xyz" || jobs[1].Quality != "" {
		t.Fatalf("jobs = %+v", jobs)
	}
	if h.tg.Count(1, "queued") != 2 {
		t.Fatalf("expected two queued acks, got %v", h.tg.Texts(1))
	}
	// The user was recorded by the middleware.
	if ids, _ := db.GetAllChatIDs(context.Background(), h.pool); len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("users = %v", ids)
	}
}

func TestHandlerAppliesQualityPreference(t *testing.T) {
	h := newBotHarness(t)
	_ = db.SetUserQuality(context.Background(), h.pool, 1, "q480")
	h.message(1, "https://youtu.be/xyz")
	if jobs := h.jobs(1); len(jobs) != 1 || jobs[0].Quality != "q480" {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestHandlerRejectsUnsafeURL(t *testing.T) {
	h := newBotHarness(t)
	h.message(1, "http://localhost:8080/health")
	if len(h.jobs(1)) != 0 {
		t.Fatal("unsafe URL was queued")
	}
	if !h.tg.AnyText(1, "not supported") {
		t.Fatalf("no rejection message: %v", h.tg.Texts(1))
	}
}

func TestHandlerQueueLimitReportsOnce(t *testing.T) {
	h := newBotHarness(t)
	h.cfg.Downly.Limits.MaxQueuedPerUser = 2
	h.message(1, "https://a.com/1 https://a.com/2 https://a.com/3 https://a.com/4 https://a.com/5")
	if n := len(h.jobs(1)); n != 2 {
		t.Fatalf("jobs = %d, want 2", n)
	}
	if n := h.tg.Count(1, "Queue limit reached"); n != 1 {
		t.Fatalf("limit message sent %d times, want 1: %v", n, h.tg.Texts(1))
	}
}

func TestHandlerDailyQuotaSkipsAdmins(t *testing.T) {
	h := newBotHarness(t)
	h.cfg.Downly.Limits.DailyQuotaPerUser = 1
	h.message(1, "https://a.com/1 https://a.com/2")
	if n := len(h.jobs(1)); n != 1 {
		t.Fatalf("user jobs = %d, want 1", n)
	}
	if !h.tg.AnyText(1, "Daily limit reached") {
		t.Fatal("no daily limit message")
	}
	h.message(adminID, "https://a.com/1 https://a.com/2")
	jobs := h.jobs(adminID)
	if len(jobs) != 2 || jobs[0].Priority != 1 {
		t.Fatalf("admin jobs = %+v", jobs)
	}
}

func TestHandlerCommandRouting(t *testing.T) {
	h := newBotHarness(t)
	h.message(1, "/help@downly_test_bot")
	if !h.tg.AnyText(1, "Send me a media link") {
		t.Fatal("/help@ourbot not handled")
	}

	before := len(h.tg.Texts(1))
	h.message(1, "/start@some_other_bot")
	h.message(1, "/banana 5")
	if after := len(h.tg.Texts(1)); after != before {
		t.Fatalf("commands for other bots / unknown commands must be ignored: %v", h.tg.Texts(1)[before:])
	}

	h.message(1, "/ban 5")
	if !h.tg.AnyText(1, "This command is for admins only.") {
		t.Fatal("non-admin /ban should be refused")
	}
	if banned, _ := db.IsBanned(context.Background(), h.pool, 5); banned {
		t.Fatal("non-admin managed to ban")
	}
	h.message(adminID, "/BAN 5 spamming")
	if banned, _ := db.IsBanned(context.Background(), h.pool, 5); !banned {
		t.Fatal("admin /BAN (any case) should ban")
	}
	h.message(5, "https://a.com/1")
	if len(h.jobs(5)) != 0 || !h.tg.AnyText(5, "banned") {
		t.Fatal("banned user could queue")
	}
}

func TestHandlerCancel(t *testing.T) {
	h := newBotHarness(t)
	h.message(1, "https://a.com/1")
	id := h.jobs(1)[0].ID
	h.message(2, fmt.Sprintf("/cancel %d", id))
	if !h.tg.AnyText(2, "doesn't belong to you") {
		t.Fatal("other user's cancel not refused")
	}
	h.message(1, fmt.Sprintf("/cancel #%d", id))
	if h.jobs(1)[0].Status != db.StatusCanceled {
		t.Fatal("job not canceled")
	}
	h.message(1, fmt.Sprintf("/cancel %d", id))
	if !h.tg.AnyText(1, "already finished") {
		t.Fatal("second cancel should say already finished")
	}
}

func TestHandlerForgedCallbacks(t *testing.T) {
	h := newBotHarness(t)
	h.callback(1, "sq:--exec=touch /tmp/pwned #")
	if q, _ := db.GetUserQuality(context.Background(), h.pool, 1); q != "best" {
		t.Fatalf("forged preference stored: %q", q)
	}
	h.callback(1, "dl:--exec=id:sometoken")
	h.callback(1, "dl:q720:unknown-token")
	if len(h.jobs(1)) != 0 {
		t.Fatal("forged quality callback queued a job")
	}

	// A genuine token still works.
	tok := pendingURLs.Put("https://youtu.be/xyz")
	h.callback(1, qualityCallbackData("q720", tok))
	if jobs := h.jobs(1); len(jobs) != 1 || jobs[0].Quality != "q720" {
		t.Fatalf("jobs = %+v", jobs)
	}
}

func TestHandlerMarksBlockedUsers(t *testing.T) {
	h := newBotHarness(t)
	h.message(1, "/start")
	h.tg.SetForbidden(1, true)
	h.message(1, "/help")
	// TouchUser clears the flag on contact, then the failed reply sets it.
	if ids, _ := db.GetAllChatIDs(context.Background(), h.pool); len(ids) != 0 {
		t.Fatalf("blocked user still targeted: %v", ids)
	}
}

func TestParseCommand(t *testing.T) {
	tests := []struct {
		in, wantName, wantArgs string
		ok                     bool
	}{
		{"/start", "start", "", true},
		{"/cancel 12", "cancel", "12", true},
		{"/Cancel@Downly_Test_Bot  12 ", "cancel", "12", true},
		{"/broadcast hello\nworld", "broadcast", "hello\nworld", true},
		{"/start@otherbot", "", "", false},
		{"/", "", "", false},
		{"hello", "", "", false},
	}
	for _, tt := range tests {
		name, args, ok := parseCommand(tt.in, "downly_test_bot")
		if name != tt.wantName || args != tt.wantArgs || ok != tt.ok {
			t.Errorf("parseCommand(%q) = %q, %q, %v", tt.in, name, args, ok)
		}
	}
}

func TestRateLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	r := newRateLimiter(10 * time.Second)
	r.now = func() time.Time { return now }
	if !r.Allow(1) || r.Allow(1) {
		t.Fatal("second call inside cooldown should be refused")
	}
	if !r.Allow(2) {
		t.Fatal("other users are independent")
	}
	now = now.Add(10 * time.Second)
	if !r.Allow(1) {
		t.Fatal("allowed after cooldown")
	}
	for i := int64(100); i < 100+2*rateLimiterPruneAt; i++ {
		r.Allow(i)
		now = now.Add(11 * time.Second)
	}
	if len(r.last) > rateLimiterPruneAt+1 {
		t.Fatalf("rate limiter grew to %d entries", len(r.last))
	}
}

func TestRunBroadcast(t *testing.T) {
	var mu sync.Mutex
	attempts := map[int64]int{}
	var blocked []int64
	send := func(_ context.Context, id int64) error {
		mu.Lock()
		defer mu.Unlock()
		attempts[id]++
		switch id {
		case 2:
			return fmt.Errorf("%w, bot was blocked by the user", bot.ErrorForbidden)
		case 3:
			if attempts[id] == 1 {
				return &bot.TooManyRequestsError{Message: "slow", RetryAfter: 0}
			}
		case 4:
			return errors.New("network")
		}
		return nil
	}
	start := time.Now()
	res := runBroadcast(context.Background(), []int64{1, 2, 3, 4, 5}, 20*time.Millisecond, send, func(id int64) { blocked = append(blocked, id) })
	if res.Sent != 3 || res.Blocked != 1 || res.Failed != 1 {
		t.Fatalf("result = %+v", res)
	}
	if attempts[3] != 2 {
		t.Fatalf("429 should be retried, attempts = %d", attempts[3])
	}
	if len(blocked) != 1 || blocked[0] != 2 {
		t.Fatalf("blocked = %v", blocked)
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond {
		t.Fatalf("broadcast not throttled: %v", elapsed)
	}
}

func TestRunBroadcastStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sent := 0
	res := runBroadcast(ctx, []int64{1, 2, 3}, time.Hour, func(context.Context, int64) error {
		sent++
		cancel()
		return nil
	}, nil)
	if sent != 1 || res.Sent != 1 || res.Failed != 2 {
		t.Fatalf("sent=%d res=%+v", sent, res)
	}
}

func TestParseJobID(t *testing.T) {
	for in, want := range map[string]int64{"12": 12, "#12": 12, " 7 extra": 7} {
		if got, ok := parseJobID(in); !ok || got != want {
			t.Errorf("parseJobID(%q) = %d, %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "abc", "-3", "0"} {
		if _, ok := parseJobID(in); ok {
			t.Errorf("parseJobID(%q) should fail", in)
		}
	}
}

package telegram

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/i18n"
)

// messageFrom sends text from a user whose Telegram client uses langCode.
func (h *botHarness) messageFrom(chat models.Chat, userID int64, langCode, text string) {
	nextUpdateID++
	h.b.ProcessUpdate(context.Background(), &models.Update{
		ID: nextUpdateID,
		Message: &models.Message{
			ID:   int(nextUpdateID),
			From: &models.User{ID: userID, FirstName: "U", LanguageCode: langCode},
			Chat: chat,
			Text: text,
		},
	})
}

func (h *botHarness) storedLanguage(chatID int64) string {
	h.t.Helper()
	code, _, err := db.GetChatLanguage(context.Background(), h.pool, chatID)
	if err != nil {
		h.t.Fatal(err)
	}
	return code
}

func (h *botHarness) lastAnswer() string {
	calls := h.tg.CallsTo("answerCallbackQuery")
	if len(calls) == 0 {
		return ""
	}
	return calls[len(calls)-1].Fields["text"]
}

func private(id int64) models.Chat { return models.Chat{ID: id, Type: models.ChatTypePrivate} }
func group(id int64) models.Chat   { return models.Chat{ID: id, Type: models.ChatTypeSupergroup} }

func TestFirstStartShowsPickerAndRemembersChoice(t *testing.T) {
	h := newBotHarness(t)
	h.message(1, "/start")

	if !h.tg.AnyText(1, "Choose your language") || !h.tg.AnyText(1, "Выберите язык") {
		t.Fatalf("first /start should show the multilingual picker, got %v", h.tg.Texts(1))
	}
	markup := h.tg.LastMarkup(1)
	for _, want := range []string{"🇬🇧 English", "🇷🇺 Русский", "🇮🇳 हिन्दी", "🇮🇷 فارسی", `"lang:ru:w"`} {
		if !strings.Contains(markup, want) {
			t.Errorf("picker missing %s: %s", want, markup)
		}
	}

	h.buttonPress(private(1), 1, "lang:ru:w")
	if got := h.storedLanguage(1); got != "ru" {
		t.Fatalf("stored language = %q", got)
	}
	if !strings.Contains(h.lastAnswer(), "Русский") {
		t.Fatalf("toast = %q", h.lastAnswer())
	}
	if !h.tg.AnyText(1, "Отправьте мне ссылку") {
		t.Fatal("help should follow in Russian after choosing")
	}
	// Per-chat command menu in the chosen language.
	var chatMenu bool
	for _, c := range h.tg.CallsTo("setMyCommands") {
		if strings.Contains(c.Fields["scope"], `"chat_id":1`) && strings.Contains(c.Fields["commands"], "Отменить загрузку") {
			chatMenu = true
		}
	}
	if !chatMenu {
		t.Fatal("chosen language should set a per-chat Russian command menu")
	}

	// A later /start shows help directly, in the remembered language.
	before := h.tg.Count(1, "Выберите язык")
	h.message(1, "/start")
	if h.tg.Count(1, "Выберите язык") != before || !strings.Contains(h.tg.LastText(1), "Отправьте мне ссылку") {
		t.Fatalf("second /start should show Russian help, got %q", h.tg.LastText(1))
	}

	// Downloads are acknowledged in Russian too.
	h.message(1, "https://youtu.be/xyz")
	if !h.tg.AnyText(1, "в очереди") {
		t.Fatalf("queue ack not in Russian: %v", h.tg.Texts(1))
	}
}

func TestSettingsMenuChangesLanguageAndQuality(t *testing.T) {
	h := newBotHarness(t)
	h.message(1, "/settings")
	if !strings.Contains(h.tg.LastText(1), "Settings") || !strings.Contains(h.tg.LastMarkup(1), `"set:lang"`) || !strings.Contains(h.tg.LastMarkup(1), `"set:quality"`) {
		t.Fatalf("settings = %q %s", h.tg.LastText(1), h.tg.LastMarkup(1))
	}

	h.buttonPress(private(1), 1, "set:lang")
	if m := h.tg.LastMarkup(1); !strings.Contains(m, `"lang:fa:s"`) || !strings.Contains(m, `"set:home"`) {
		t.Fatalf("language page = %s", m)
	}
	h.buttonPress(private(1), 1, "lang:fa:s")
	if h.storedLanguage(1) != "fa" {
		t.Fatal("language not saved")
	}
	if got := h.tg.LastText(1); !strings.Contains(got, "تنظیمات") || !strings.Contains(got, "فارسی") {
		t.Fatalf("settings should re-render in Persian, got %q", got)
	}

	h.buttonPress(private(1), 1, "set:quality")
	if m := h.tg.LastMarkup(1); !strings.Contains(m, `"sq:q720:s"`) || !strings.Contains(m, "بازگشت") {
		t.Fatalf("quality page = %s", m)
	}
	h.buttonPress(private(1), 1, "sq:q720:s")
	if q, _ := db.GetUserQuality(context.Background(), h.pool, 1); q != "q720" {
		t.Fatalf("quality = %q", q)
	}
	if got := h.tg.LastText(1); !strings.Contains(got, "720p") || !strings.Contains(got, "تنظیمات") {
		t.Fatalf("should return to settings showing 720p, got %q", got)
	}

	// Changing again later works and replaces the old value.
	h.buttonPress(private(1), 1, "lang:hi:s")
	if h.storedLanguage(1) != "hi" || !strings.Contains(h.tg.LastText(1), "सेटिंग्स") {
		t.Fatalf("change to Hindi failed: %q", h.tg.LastText(1))
	}
}

func TestLanguageCommandOpensPicker(t *testing.T) {
	h := newBotHarness(t)
	h.message(1, "/language")
	if !strings.Contains(h.tg.LastMarkup(1), `"lang:en:s"`) {
		t.Fatalf("markup = %s", h.tg.LastMarkup(1))
	}
}

func TestForgedLanguageIsRejected(t *testing.T) {
	h := newBotHarness(t)
	for _, data := range []string{"lang:xx:w", "lang:ru:zz", "lang:", "lang:ru"} {
		h.buttonPress(private(1), 1, data)
	}
	if got := h.storedLanguage(1); got != "" {
		t.Fatalf("forged callback stored %q", got)
	}
}

func TestGroupLanguageNeedsGroupAdmin(t *testing.T) {
	h := newBotHarness(t)
	const g = -100123
	h.groupMessage(g, 5, "/settings")
	if !strings.Contains(h.tg.LastText(g), "Group settings") || strings.Contains(h.tg.LastMarkup(g), "set:quality") {
		t.Fatalf("group settings = %q %s", h.tg.LastText(g), h.tg.LastMarkup(g))
	}

	h.buttonPress(group(g), 5, "lang:hi:s")
	if h.storedLanguage(g) != "" {
		t.Fatal("a regular member changed the group language")
	}
	if !strings.Contains(h.lastAnswer(), "group admins") {
		t.Fatalf("toast = %q", h.lastAnswer())
	}

	h.tg.SetChatAdmin(g, 6)
	h.buttonPress(group(g), 6, "lang:hi:s")
	if h.storedLanguage(g) != "hi" {
		t.Fatal("group admin could not change the language")
	}

	// Bot admins can always change it.
	h.buttonPress(group(g), adminID, "lang:ru:s")
	if h.storedLanguage(g) != "ru" {
		t.Fatal("bot admin could not change the group language")
	}

	// The group's language wins over a member's personal choice.
	_ = db.SetChatLanguage(context.Background(), h.pool, 5, "fa")
	h.groupMessage(g, 5, "/help")
	if !strings.Contains(h.tg.LastText(g), "Отправьте мне ссылку") {
		t.Fatalf("group reply should be Russian, got %q", h.tg.LastText(g))
	}
	// The member's own private chat stays Persian.
	h.message(5, "/help")
	if !strings.Contains(h.tg.LastText(5), "یک لینک رسانه") {
		t.Fatalf("private reply should be Persian, got %q", h.tg.LastText(5))
	}
}

func TestLanguageFallbacks(t *testing.T) {
	h := newBotHarness(t)
	const g = -100777

	// Nothing chosen: guess from the Telegram client language.
	h.messageFrom(private(7), 7, "fa-IR", "/help")
	if !strings.Contains(h.tg.LastText(7), "یک لینک رسانه") {
		t.Fatalf("expected Persian from client language, got %q", h.tg.LastText(7))
	}
	h.messageFrom(private(8), 8, "de", "/help")
	if !strings.Contains(h.tg.LastText(8), "Send me a media link") {
		t.Fatalf("unsupported client language should fall back to English, got %q", h.tg.LastText(8))
	}

	// Group without a language: the member's own choice, then their client language.
	_ = db.SetChatLanguage(context.Background(), h.pool, 9, "hi")
	h.messageFrom(group(g), 9, "en", "/help")
	if !strings.Contains(h.tg.LastText(g), "मुझे कोई मीडिया लिंक") {
		t.Fatalf("expected the member's Hindi in an unconfigured group, got %q", h.tg.LastText(g))
	}
}

func TestBotAddedToGroupAsksForLanguage(t *testing.T) {
	h := newBotHarness(t)
	const g = -100555
	added := func() {
		nextUpdateID++
		h.b.ProcessUpdate(context.Background(), &models.Update{
			ID: nextUpdateID,
			MyChatMember: &models.ChatMemberUpdated{
				Chat:          group(g),
				From:          models.User{ID: 5},
				OldChatMember: models.ChatMember{Type: models.ChatMemberTypeLeft, Left: &models.ChatMemberLeft{}},
				NewChatMember: models.ChatMember{Type: models.ChatMemberTypeMember, Member: &models.ChatMemberMember{}},
			},
		})
	}
	added()
	if !h.tg.AnyText(g, "Choose your language") || !strings.Contains(h.tg.LastMarkup(g), `"lang:fa:w"`) {
		t.Fatalf("no picker after joining: %v", h.tg.Texts(g))
	}

	h.tg.SetChatAdmin(g, 5)
	h.buttonPress(group(g), 5, "lang:fa:w")
	if h.storedLanguage(g) != "fa" || !h.tg.AnyText(g, "یک لینک رسانه") {
		t.Fatalf("welcome choice failed: %v", h.tg.Texts(g))
	}

	// Re-adding a group that already chose does not ask again.
	before := h.tg.Count(g, "Choose your language")
	added()
	if h.tg.Count(g, "Choose your language") != before {
		t.Fatal("picker shown again for a configured group")
	}
}

func TestCommandMenusPublishedPerLanguage(t *testing.T) {
	h := newBotHarness(t)
	SetCommandMenus(context.Background(), h.b, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	calls := h.tg.CallsTo("setMyCommands")
	if len(calls) != 1+len(i18n.Languages) {
		t.Fatalf("setMyCommands calls = %d", len(calls))
	}
	seen := map[string]string{}
	for _, c := range calls {
		seen[c.Fields["language_code"]] = c.Fields["commands"]
	}
	if !strings.Contains(seen["ru"], "Отменить загрузку") || !strings.Contains(seen["fa"], "لغو دانلود") ||
		!strings.Contains(seen["hi"], "डाउनलोड रद्द करें") || !strings.Contains(seen[""], "Cancel a download") {
		t.Fatalf("menus = %v", seen)
	}
}

func TestQueueAndHistoryAreLocalized(t *testing.T) {
	h := newBotHarness(t)
	_ = db.SetChatLanguage(context.Background(), h.pool, 1, "ru")
	h.message(1, "/queue")
	if !strings.Contains(h.tg.LastText(1), "нет недавних загрузок") {
		t.Fatalf("empty queue = %q", h.tg.LastText(1))
	}
	h.message(1, "https://youtu.be/xyz")
	h.message(1, "/queue")
	if got := h.tg.LastText(1); !strings.Contains(got, "Ваши недавние загрузки") || !strings.Contains(got, "в очереди") || !strings.Contains(got, "место 1") {
		t.Fatalf("queue = %q", got)
	}
	h.message(1, "/history")
	if !strings.Contains(h.tg.LastText(1), "История загрузок пока пуста") {
		t.Fatalf("history = %q", h.tg.LastText(1))
	}
}

package telegram

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/i18n"
	"github.com/meanii/downly/internal/safeurl"
	"github.com/meanii/downly/internal/worker"
)

const repoURL = "https://github.com/meanii/downly"

// maxMessageLen is Telegram's limit for a text message.
const maxMessageLen = 4096

// handler bundles what every update handler needs.
type handler struct {
	log        *slog.Logger
	cfg        *config.Root
	controller *worker.Controller
	b          *bot.Bot
	pool       *pgxpool.Pool
	limiter    *rateLimiter
	commands   map[string]commandFunc
	msgr       worker.Messenger
}

// request is a parsed command invocation.
type request struct {
	msg  *models.Message
	args string
	lang i18n.Lang
}

// commandFunc handles "/name args".
type commandFunc func(ctx context.Context, r *request)

func RegisterHandlers(logger *slog.Logger, cfg *config.Root, controller *worker.Controller, b *bot.Bot, pool *pgxpool.Pool) {
	h := &handler{
		log:        logger.With("component", "telegram"),
		cfg:        cfg,
		controller: controller,
		b:          b,
		pool:       pool,
		limiter:    newRateLimiter(time.Duration(cfg.Downly.Limits.RateLimitSeconds) * time.Second),
		msgr:       worker.TelegramMessenger{Bot: b},
	}
	h.commands = h.commandTable()

	b.RegisterHandler(bot.HandlerTypeMessageText, "", bot.MatchTypeContains, h.onMessage)
	b.RegisterHandlerMatchFunc(func(u *models.Update) bool {
		return u.CallbackQuery != nil && strings.HasPrefix(u.CallbackQuery.Data, "dl:")
	}, h.onQualityCallback)
	b.RegisterHandlerMatchFunc(func(u *models.Update) bool {
		return u.CallbackQuery != nil && strings.HasPrefix(u.CallbackQuery.Data, "sq:")
	}, h.onSetQualityCallback)
	b.RegisterHandlerMatchFunc(func(u *models.Update) bool {
		return u.CallbackQuery != nil && (strings.HasPrefix(u.CallbackQuery.Data, "lang:") || strings.HasPrefix(u.CallbackQuery.Data, "set:"))
	}, h.onSettingsCallback)
	b.RegisterHandlerMatchFunc(func(u *models.Update) bool { return u.MyChatMember != nil }, h.onMyChatMember)
	b.RegisterHandlerMatchFunc(func(u *models.Update) bool {
		return u.CallbackQuery != nil && strings.HasPrefix(u.CallbackQuery.Data, "again:")
	}, h.onAgainCallback)
	b.RegisterHandlerMatchFunc(func(u *models.Update) bool {
		return u.CallbackQuery != nil && u.CallbackQuery.Data == "noop"
	}, h.onNoopCallback)
	b.RegisterHandlerMatchFunc(func(u *models.Update) bool { return u.InlineQuery != nil }, h.onInlineQuery)
	b.RegisterHandlerMatchFunc(func(u *models.Update) bool { return u.ChosenInlineResult != nil }, h.onChosenInlineResult)
}

func (h *handler) onMessage(ctx context.Context, b *bot.Bot, update *models.Update) {
	msg := update.Message
	if msg == nil || msg.Text == "" || msg.From == nil || msg.From.IsBot {
		return
	}
	text := strings.TrimSpace(msg.Text)

	if strings.HasPrefix(text, "/") {
		name, args, ok := parseCommand(text, getBotUsername(ctx, b))
		if !ok {
			return // addressed to another bot
		}
		if cmd, found := h.commands[name]; found {
			h.log.Info("command", "command", name, "chat_id", msg.Chat.ID, "user_id", msg.From.ID)
			cmd(ctx, &request{msg: msg, args: args, lang: h.langFor(ctx, msg.Chat.ID, msg.From)})
		}
		return
	}

	reqs := extractRequests(text)
	if len(reqs) == 0 {
		return
	}
	if isGroup(msg.Chat) {
		mode, err := db.GetGroupMode(ctx, h.pool, msg.Chat.ID)
		if err != nil {
			h.log.Warn("load group mode failed", "chat_id", msg.Chat.ID, "error", err)
		}
		if mode == db.GroupModeCommand {
			return // this group only downloads via /dl
		}
	}
	h.downloadAll(ctx, msg, reqs, msg.ID, false)
}

// downloadAll queues every request from msg (as GIFs when gif is set). In
// groups the bot answers replyTo (the message holding the links); in
// private chats it doesn't thread replies.
func (h *handler) downloadAll(ctx context.Context, msg *models.Message, reqs []urlRequest, replyTo int, gif bool) {
	chatID, userID := msg.Chat.ID, msg.From.ID
	lang := h.langFor(ctx, chatID, msg.From)
	if !isGroup(msg.Chat) {
		replyTo = 0
	}
	if !h.allowSubmit(ctx, chatID, userID, lang) {
		return
	}
	quality := h.preferredQuality(ctx, userID)
	for _, req := range reqs {
		if text, ok := checkClip(lang, req.clip, gif); !ok {
			h.replyTo(ctx, chatID, replyTo, text)
			return
		}
		url := req.url
		// Apply the user's quality preference if no explicit prefix
		if _, prefix := stripModePrefix(url); prefix == "" && quality != "" && !gif {
			url = quality + ":" + url
		}
		d := download{chatID: chatID, userID: userID, lang: lang, url: url, replyTo: replyTo, clip: req.clip, gif: gif}
		if _, err := h.enqueue(ctx, d); err != nil {
			// Limit and validation errors were already reported; stop on the
			// first so a long list does not produce a wall of errors.
			return
		}
	}
}

func isGroup(chat models.Chat) bool {
	return chat.Type == models.ChatTypeGroup || chat.Type == models.ChatTypeSupergroup
}

// parseCommand splits "/name@bot args" into ("name", "args"). ok is false
// if the command is addressed to a different bot.
func parseCommand(text, botUsername string) (name, args string, ok bool) {
	if !strings.HasPrefix(text, "/") {
		return "", "", false
	}
	head, rest := text, ""
	if i := strings.IndexFunc(text, unicode.IsSpace); i >= 0 {
		head, rest = text[:i], text[i:]
	}
	name = head[1:]
	if at := strings.IndexByte(name, '@'); at >= 0 {
		target := name[at+1:]
		name = name[:at]
		if botUsername != "" && !strings.EqualFold(target, botUsername) {
			return "", "", false
		}
	}
	if name == "" {
		return "", "", false
	}
	return strings.ToLower(name), strings.TrimSpace(rest), true
}

// reply sends text, logging failures and noting users who blocked the bot.
func (h *handler) reply(ctx context.Context, chatID int64, text string) {
	h.replyTo(ctx, chatID, 0, text)
}

// replyTo is reply as an answer to message replyTo (0 = none).
func (h *handler) replyTo(ctx context.Context, chatID int64, replyTo int, text string) {
	if _, err := h.send(ctx, &bot.SendMessageParams{ChatID: chatID, Text: text, ReplyParameters: replyParams(replyTo)}); err != nil {
		h.log.Warn("send message failed", "chat_id", chatID, "error", err)
	}
}

func replyParams(messageID int) *models.ReplyParameters {
	if messageID == 0 {
		return nil
	}
	return &models.ReplyParameters{MessageID: messageID, AllowSendingWithoutReply: true}
}

func (h *handler) send(ctx context.Context, params *bot.SendMessageParams) (*models.Message, error) {
	params.Text = truncateRunes(params.Text, maxMessageLen)
	m, err := h.b.SendMessage(ctx, params)
	if errors.Is(err, bot.ErrorForbidden) {
		if chatID, ok := params.ChatID.(int64); ok {
			if dbErr := db.MarkUserBlocked(ctx, h.pool, chatID); dbErr != nil {
				h.log.Warn("mark user blocked failed", "chat_id", chatID, "error", dbErr)
			}
		}
	}
	return m, err
}

func (h *handler) edit(ctx context.Context, chatID int64, messageID int, text string) {
	_, err := h.b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: messageID, Text: truncateRunes(text, maxMessageLen)})
	if err != nil && !strings.Contains(err.Error(), "message is not modified") {
		h.log.Warn("edit message failed", "chat_id", chatID, "message_id", messageID, "error", err)
	}
}

func (h *handler) answerCallback(ctx context.Context, id, text string) {
	if _, err := h.b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{CallbackQueryID: id, Text: text}); err != nil {
		h.log.Warn("answer callback failed", "error", err)
	}
}

// allowSubmit applies the ban check and rate limit to a new download request,
// telling the user when they are refused.
func (h *handler) allowSubmit(ctx context.Context, chatID, userID int64, lang i18n.Lang) bool {
	banned, err := db.IsBanned(ctx, h.pool, userID)
	if err != nil {
		// Fail open: a DB hiccup shouldn't lock everyone out.
		h.log.Error("ban check failed", "user_id", userID, "error", err)
	}
	if banned {
		h.reply(ctx, chatID, i18n.T(lang, "banned"))
		return false
	}
	if !h.limiter.Allow(userID) {
		h.reply(ctx, chatID, i18n.T(lang, "slow_down"))
		return false
	}
	return true
}

// langFor picks the language for replies in chatID to user: the chat's
// chosen language, then (in groups) the user's own choice, then a guess from
// the user's Telegram client language.
func (h *handler) langFor(ctx context.Context, chatID int64, user *models.User) i18n.Lang {
	if l, ok := h.storedLang(ctx, chatID); ok {
		return l
	}
	if user == nil {
		return i18n.Default
	}
	if user.ID != chatID {
		if l, ok := h.storedLang(ctx, user.ID); ok {
			return l
		}
	}
	return i18n.Guess(user.LanguageCode)
}

// storedLang returns the language explicitly chosen for chatID, if any.
func (h *handler) storedLang(ctx context.Context, chatID int64) (i18n.Lang, bool) {
	code, ok, err := db.GetChatLanguage(ctx, h.pool, chatID)
	if err != nil {
		h.log.Warn("load chat language failed", "chat_id", chatID, "error", err)
		return "", false
	}
	if !ok {
		return "", false
	}
	return i18n.Parse(code)
}

// preferredQuality returns the user's saved quality prefix ("q720"), or "".
func (h *handler) preferredQuality(ctx context.Context, userID int64) string {
	q, err := db.GetUserQuality(ctx, h.pool, userID)
	if err != nil {
		h.log.Warn("load quality preference failed", "user_id", userID, "error", err)
	}
	if downloader.ValidQuality(q) && q != "best" && q != "qbest" {
		return q
	}
	return ""
}

func (h *handler) limitsFor(userID int64) db.EnqueueLimits {
	lim := db.EnqueueLimits{MaxQueued: h.cfg.Downly.Limits.MaxQueuedPerUser}
	if !isAdmin(h.cfg, userID) {
		lim.DailyQuota = h.cfg.Downly.Limits.DailyQuotaPerUser
	}
	return lim
}

func limitMessage(lang i18n.Lang, le *db.LimitError) string {
	switch le.Kind {
	case db.LimitDaily:
		return i18n.T(lang, "limit_daily", le.Count, le.Limit)
	default:
		return i18n.T(lang, "limit_queue", le.Count)
	}
}

// rateLimiter enforces a per-user cooldown between submissions. Memory is
// bounded: entries older than the cooldown are pruned as the map grows.
type rateLimiter struct {
	mu       sync.Mutex
	cooldown time.Duration
	last     map[int64]time.Time
	now      func() time.Time
}

const rateLimiterPruneAt = 1024

func newRateLimiter(cooldown time.Duration) *rateLimiter {
	return &rateLimiter{cooldown: cooldown, last: make(map[int64]time.Time), now: time.Now}
}

func (r *rateLimiter) Allow(userID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	if t, ok := r.last[userID]; ok && now.Sub(t) < r.cooldown {
		return false
	}
	if len(r.last) >= rateLimiterPruneAt {
		for id, t := range r.last {
			if now.Sub(t) >= r.cooldown {
				delete(r.last, id)
			}
		}
	}
	r.last[userID] = now
	return true
}

var (
	botUsernameMu     sync.Mutex
	cachedBotUsername string
)

func getBotUsername(ctx context.Context, b *bot.Bot) string {
	botUsernameMu.Lock()
	defer botUsernameMu.Unlock()
	if cachedBotUsername != "" {
		return cachedBotUsername
	}
	me, err := b.GetMe(ctx)
	if err == nil && me.Username != "" {
		cachedBotUsername = me.Username
	}
	return cachedBotUsername
}

func isAdmin(cfg *config.Root, userID int64) bool {
	for _, id := range cfg.Downly.Admin.UserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func parseJobID(args string) (int64, bool) {
	parts := strings.Fields(args)
	if len(parts) < 1 {
		return 0, false
	}
	jobID, err := strconv.ParseInt(strings.TrimPrefix(parts[0], "#"), 10, 64)
	if err != nil || jobID <= 0 {
		return 0, false
	}
	return jobID, true
}

// truncateRunes shortens s to at most max runes without breaking UTF-8.
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

func truncateStr(s string, max int) string { return truncateRunes(s, max) }

// modePrefixes are the job-mode markers a URL may carry.
var modePrefixes = []string{"audio:", "telegram:", "q360:", "q480:", "q720:", "q1080:"}

// isQueueableURL reports whether a (possibly mode-prefixed) URL is safe to hand to the downloader.
func isQueueableURL(raw string) bool {
	clean, _ := stripModePrefix(raw)
	_, err := safeurl.Validate(clean)
	return err == nil
}

func isPreferenceValue(q string) bool {
	for _, p := range qualityPreferences {
		if p.Value == q {
			return true
		}
	}
	return false
}

// jobSpec turns a possibly prefixed URL ("audio:<url>", "q720:<url>") into
// the URL plus the job's mode and quality.
func jobSpec(raw string) (url string, mode db.JobMode, quality string) {
	clean, prefix := stripModePrefix(raw)
	switch prefix {
	case "":
		return clean, db.ModeVideo, ""
	case "audio:":
		return clean, db.ModeAudio, ""
	default:
		return clean, db.ModeVideo, strings.TrimSuffix(prefix, ":")
	}
}

// stripModePrefix removes quality/audio prefixes from a word, returning the clean word and the prefix.
func stripModePrefix(word string) (clean, prefix string) {
	for _, p := range modePrefixes {
		if strings.HasPrefix(word, p) {
			return strings.TrimPrefix(word, p), p
		}
	}
	return word, ""
}

// extractURLs finds all URLs in a text message, including quality/audio-prefixed URLs.
func extractURLs(text string) []string {
	var urls []string
	for _, word := range strings.Fields(text) {
		clean, prefix := stripModePrefix(word)
		normalized := normalizeURL(clean)
		if looksLikeURL(normalized) {
			urls = append(urls, prefix+normalized)
		}
	}
	return urls
}

// containsURL checks if the text contains at least one URL-like string.
func containsURL(text string) bool {
	return len(extractURLs(text)) > 0
}

func trimURL(s string) string { return truncateRunes(s, 60) }

func looksLikeURL(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "www.")
}

func normalizeURL(s string) string {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "www.") {
		return "https://" + s
	}
	return s
}

// qualityCallbackData builds "dl:<quality>:<token>", which always fits in
// Telegram's 64-byte callback_data limit.
func qualityCallbackData(quality, token string) string {
	return "dl:" + quality + ":" + token
}

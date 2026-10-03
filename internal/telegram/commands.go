package telegram

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/i18n"
)

func (h *handler) commandTable() map[string]commandFunc {
	user := map[string]commandFunc{
		"start":      h.cmdStart,
		"help":       h.cmdHelp,
		"queue":      h.cmdQueue,
		"history":    h.cmdHistory,
		"cancel":     h.cmdCancel,
		"mp3":        h.cmdMP3,
		"setquality": h.cmdSetQuality,
		"quality":    h.cmdQuality,
		"playlist":   h.cmdPlaylist,
		"settings":   h.cmdSettings,
		"language":   h.cmdLanguage,
		"priority": func(ctx context.Context, r *request) {
			h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "priority_info"))
		},
	}
	admin := map[string]commandFunc{
		"stats":     h.cmdStats,
		"health":    h.cmdHealth,
		"bandwidth": h.cmdBandwidth,
		"users":     h.cmdUsers,
		"jobs":      h.cmdJobs,
		"promote":   func(ctx context.Context, r *request) { h.setPriority(ctx, r, 10) },
		"demote":    func(ctx context.Context, r *request) { h.setPriority(ctx, r, 0) },
		"broadcast": h.cmdBroadcast,
		"ban":       h.cmdBan,
		"unban":     h.cmdUnban,
	}
	for name, fn := range admin {
		user[name] = h.adminOnly(fn)
	}
	return user
}

func (h *handler) adminOnly(fn commandFunc) commandFunc {
	return func(ctx context.Context, r *request) {
		if !isAdmin(h.cfg, r.msg.From.ID) {
			h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "admin_only"))
			return
		}
		fn(ctx, r)
	}
}

// cmdStart shows the language picker on first contact, help otherwise.
func (h *handler) cmdStart(ctx context.Context, r *request) {
	if _, chosen := h.storedLang(ctx, r.msg.Chat.ID); !chosen {
		h.sendLanguagePicker(ctx, r.msg.Chat, originWelcome)
		return
	}
	h.cmdHelp(ctx, r)
}

func (h *handler) cmdHelp(ctx context.Context, r *request) {
	h.reply(ctx, r.msg.Chat.ID, h.helpText(ctx, r.lang, r.msg.From.ID))
}

// adminHelp is appended to the help for admins; admin tooling is English-only.
const adminHelp = "Admin commands:\n" +
	"/stats - bot analytics\n" +
	"/health - platform health dashboard\n" +
	"/bandwidth [limit] - user bandwidth/storage report\n" +
	"/users - list all users\n" +
	"/jobs - active and pending jobs\n" +
	"/promote, /demote <job_id> - change priority\n" +
	"/broadcast <msg> - message all users\n" +
	"/ban, /unban <user_id> - block/unblock user"

func (h *handler) helpText(ctx context.Context, lang i18n.Lang, userID int64) string {
	text := i18n.T(lang, "start", getBotUsername(ctx, h.b))
	if isAdmin(h.cfg, userID) {
		text += "\n\n" + adminHelp
	}
	return text + "\n\nRepo: " + repoURL
}

func (h *handler) cmdQueue(ctx context.Context, r *request) {
	jobs, err := db.GetUserJobs(ctx, h.pool, r.msg.From.ID, 10)
	if err != nil {
		h.log.Error("list user jobs failed", "user_id", r.msg.From.ID, "error", err)
		h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "generic_error"))
		return
	}
	h.reply(ctx, r.msg.Chat.ID, formatUserQueue(r.lang, jobs))
}

func (h *handler) cmdHistory(ctx context.Context, r *request) {
	jobs, err := db.GetUserHistory(ctx, h.pool, r.msg.From.ID, 15)
	if err != nil {
		h.log.Error("load history failed", "user_id", r.msg.From.ID, "error", err)
		h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "generic_error"))
		return
	}
	h.reply(ctx, r.msg.Chat.ID, formatUserHistory(r.lang, jobs))
}

func (h *handler) cmdCancel(ctx context.Context, r *request) {
	chatID, userID, lang := r.msg.Chat.ID, r.msg.From.ID, r.lang
	jobID, ok := parseJobID(r.args)
	if !ok {
		h.reply(ctx, chatID, i18n.T(lang, "cancel_usage"))
		return
	}
	prev, err := db.CancelJob(ctx, h.pool, jobID, userID)
	if err != nil {
		h.log.Error("cancel job failed", "job_id", jobID, "error", err)
		h.reply(ctx, chatID, i18n.T(lang, "generic_error"))
		return
	}
	switch prev {
	case db.StatusPending:
		h.reply(ctx, chatID, i18n.T(lang, "canceled_pending", jobID))
	case db.StatusProcessing:
		// Stop it right away if it runs here; a worker in another instance
		// notices the canceled status on its next heartbeat.
		h.controller.Cancel(jobID)
		h.reply(ctx, chatID, i18n.T(lang, "canceled_running", jobID))
	default:
		owns, _, err := db.OwnsJob(ctx, h.pool, jobID, userID)
		switch {
		case err != nil:
			h.log.Error("inspect job failed", "job_id", jobID, "error", err)
			h.reply(ctx, chatID, i18n.T(lang, "generic_error"))
		case !owns:
			h.reply(ctx, chatID, i18n.T(lang, "cancel_not_yours"))
		default:
			h.reply(ctx, chatID, i18n.T(lang, "cancel_finished"))
		}
	}
}

func (h *handler) cmdMP3(ctx context.Context, r *request) {
	fields := strings.Fields(r.args)
	if len(fields) < 1 {
		h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "mp3_usage"))
		return
	}
	url := normalizeURL(fields[0])
	if !looksLikeURL(url) {
		h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "invalid_url"))
		return
	}
	if !h.allowSubmit(ctx, r.msg.Chat.ID, r.msg.From.ID, r.lang) {
		return
	}
	_, _ = h.enqueue(ctx, r.msg.Chat.ID, r.msg.From.ID, r.lang, "audio:"+url)
}

// Quality options for the one-off /quality picker.
var qualityOptions = []struct {
	Label    string // "" means the translated "Best"
	Callback string
}{
	{"📱 Telegram", "telegram"},
	{"360p", "q360"},
	{"480p", "q480"},
	{"720p", "q720"},
	{"1080p", "q1080"},
	{"", "qbest"},
}

func (h *handler) cmdQuality(ctx context.Context, r *request) {
	fields := strings.Fields(r.args)
	if len(fields) < 1 {
		h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "quality_usage"))
		return
	}
	url := normalizeURL(fields[0])
	if !isQueueableURL(url) {
		h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "invalid_url"))
		return
	}

	// The URL is too long for callback_data, so buttons carry a short token.
	token := pendingURLs.Put(url)
	var buttons []models.InlineKeyboardButton
	for _, q := range qualityOptions {
		label := q.Label
		if label == "" {
			label = i18n.T(r.lang, "quality_best")
		}
		buttons = append(buttons, models.InlineKeyboardButton{Text: label, CallbackData: qualityCallbackData(q.Callback, token)})
	}
	if _, err := h.send(ctx, &bot.SendMessageParams{
		ChatID:      r.msg.Chat.ID,
		Text:        i18n.T(r.lang, "quality_pick", trimURL(url)),
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{buttons}},
	}); err != nil {
		h.log.Warn("send quality picker failed", "error", err)
	}
}

// Quality preference options for /setquality.
var qualityPreferences = []struct {
	Label string // "" means the translated "Best (default)"
	Value string
}{
	{"360p", "q360"},
	{"480p", "q480"},
	{"720p", "q720"},
	{"1080p", "q1080"},
	{"", "best"},
}

// qualityPreferenceKeyboard lists the qualities with a check on current.
// fromSettings adds a back button to the settings menu.
func qualityPreferenceKeyboard(lang i18n.Lang, current string, fromSettings bool) *models.InlineKeyboardMarkup {
	origin := ""
	if fromSettings {
		origin = ":" + originSettings
	}
	var rows [][]models.InlineKeyboardButton
	for _, q := range qualityPreferences {
		label := qualityLabel(lang, q.Value)
		if q.Value == current {
			label = "✓ " + label
		}
		rows = append(rows, []models.InlineKeyboardButton{{Text: label, CallbackData: "sq:" + q.Value + origin}})
	}
	if fromSettings {
		rows = append(rows, []models.InlineKeyboardButton{{Text: i18n.T(lang, "btn_back"), CallbackData: "set:home"}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func qualityLabel(lang i18n.Lang, value string) string {
	for _, q := range qualityPreferences {
		if q.Value == value && q.Label != "" {
			return q.Label
		}
	}
	return i18n.T(lang, "quality_best_default")
}

func (h *handler) cmdSetQuality(ctx context.Context, r *request) {
	current := h.currentQuality(ctx, r.msg.From.ID)
	if _, err := h.send(ctx, &bot.SendMessageParams{
		ChatID:      r.msg.Chat.ID,
		Text:        i18n.T(r.lang, "quality_current", qualityLabel(r.lang, current)),
		ReplyMarkup: qualityPreferenceKeyboard(r.lang, current, false),
	}); err != nil {
		h.log.Warn("send quality preferences failed", "error", err)
	}
}

func (h *handler) currentQuality(ctx context.Context, userID int64) string {
	q, err := db.GetUserQuality(ctx, h.pool, userID)
	if err != nil {
		h.log.Warn("load quality preference failed", "user_id", userID, "error", err)
	}
	return q
}

const (
	playlistDefault = 10
	playlistMax     = 25
)

func (h *handler) cmdPlaylist(ctx context.Context, r *request) {
	chatID, userID, lang := r.msg.Chat.ID, r.msg.From.ID, r.lang
	fields := strings.Fields(r.args)
	if len(fields) < 1 {
		h.reply(ctx, chatID, i18n.T(lang, "playlist_usage", playlistDefault, playlistMax))
		return
	}
	url := normalizeURL(fields[0])
	if !isQueueableURL(url) {
		h.reply(ctx, chatID, i18n.T(lang, "invalid_url"))
		return
	}
	requested := playlistDefault
	if len(fields) >= 2 {
		if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 {
			requested = n
		}
	}
	requested = min(requested, playlistMax)

	if !h.allowSubmit(ctx, chatID, userID, lang) {
		return
	}

	// Only fetch and queue what the user's limits leave room for, instead of
	// queueing until the limit trips once per remaining entry.
	room, err := db.QueueRoom(ctx, h.pool, userID, h.limitsFor(userID))
	if err != nil {
		h.log.Error("queue room check failed", "user_id", userID, "error", err)
		h.reply(ctx, chatID, i18n.T(lang, "generic_error"))
		return
	}
	if room == 0 {
		h.reply(ctx, chatID, i18n.T(lang, "playlist_full"))
		return
	}
	want := requested
	if room > 0 {
		want = min(want, room)
	}

	h.reply(ctx, chatID, i18n.T(lang, "playlist_fetching"))
	dl := downloader.YTDLP{Bin: h.cfg.Downly.Services.YTDLP.Bin, CookiesFile: h.cfg.Downly.Services.YTDLP.CookiesFile, Logger: h.log}
	entries, playlistTitle, err := dl.FetchPlaylist(ctx, url, want)
	if err != nil {
		h.log.Warn("fetch playlist failed", "url", url, "error", err)
		h.reply(ctx, chatID, i18n.T(lang, "playlist_fetch_failed", truncateStr(err.Error(), 200)))
		return
	}
	if len(entries) == 0 {
		h.reply(ctx, chatID, i18n.T(lang, "playlist_empty"))
		return
	}

	summary := i18n.T(lang, "playlist_summary", truncateStr(playlistTitle, 80), len(entries)) + "\n"
	for i, e := range entries {
		summary += fmt.Sprintf("\n%d. %s", i+1, truncateStr(e.Title, 60))
	}
	if room > 0 && room < requested {
		summary += "\n\n" + i18n.T(lang, "playlist_partial", room, requested)
	}
	h.reply(ctx, chatID, summary)

	quality := h.preferredQuality(ctx, userID)
	queued := 0
	for _, e := range entries {
		u := e.URL
		if quality != "" {
			u = quality + ":" + u
		}
		if _, err := h.enqueue(ctx, chatID, userID, lang, u); err != nil {
			if _, isLimit := db.IsLimit(err); isLimit {
				break
			}
			continue
		}
		queued++
	}
	h.reply(ctx, chatID, i18n.T(lang, "playlist_done", queued, len(entries)))
}

// --- Admin (English-only output) ---

func (h *handler) cmdStats(ctx context.Context, r *request) {
	stats, err := db.GetBotStats(ctx, h.pool)
	if err != nil {
		h.log.Error("load stats failed", "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Failed to load stats.")
		return
	}
	topUsers, err := db.GetTopUsers(ctx, h.pool, 5)
	if err != nil {
		h.log.Warn("load top users failed", "error", err)
	}
	topPlatforms, err := db.GetTopPlatforms(ctx, h.pool, 5)
	if err != nil {
		h.log.Warn("load top platforms failed", "error", err)
	}
	h.reply(ctx, r.msg.Chat.ID, db.FormatBotStats(stats, topUsers, topPlatforms))
}

func (h *handler) cmdHealth(ctx context.Context, r *request) {
	platforms, err := db.GetPlatformHealth(ctx, h.pool, 24, 15)
	if err != nil {
		h.log.Error("load platform health failed", "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Failed to load platform health.")
		return
	}
	h.reply(ctx, r.msg.Chat.ID, db.FormatPlatformHealth(platforms, 24))
}

func (h *handler) cmdBandwidth(ctx context.Context, r *request) {
	limit := 20
	if fields := strings.Fields(r.args); len(fields) > 0 {
		if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 {
			limit = min(n, 100)
		}
	}
	users, err := db.GetUserBandwidth(ctx, h.pool, limit)
	if err != nil {
		h.log.Error("load bandwidth failed", "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Failed to load bandwidth data.")
		return
	}
	h.reply(ctx, r.msg.Chat.ID, db.FormatUserBandwidth(users))
}

func (h *handler) cmdUsers(ctx context.Context, r *request) {
	users, err := db.GetAllUsers(ctx, h.pool, 25)
	if err != nil {
		h.log.Error("load users failed", "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Failed to load user list.")
		return
	}
	h.reply(ctx, r.msg.Chat.ID, db.FormatUserList(users))
}

func (h *handler) cmdJobs(ctx context.Context, r *request) {
	jobs, err := db.GetActiveJobs(ctx, h.pool, 20)
	if err != nil {
		h.log.Error("load active jobs failed", "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Failed to load job list.")
		return
	}
	h.reply(ctx, r.msg.Chat.ID, db.FormatActiveJobs(jobs))
}

func (h *handler) setPriority(ctx context.Context, r *request, priority int) {
	jobID, ok := parseJobID(r.args)
	if !ok {
		h.reply(ctx, r.msg.Chat.ID, "Usage: /promote <job_id> or /demote <job_id>")
		return
	}
	updated, err := db.UpdatePriority(ctx, h.pool, jobID, priority)
	if err != nil {
		h.log.Error("update priority failed", "job_id", jobID, "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Failed to update priority right now.")
		return
	}
	if !updated {
		h.reply(ctx, r.msg.Chat.ID, "Only pending jobs can have priority changed.")
		return
	}
	h.reply(ctx, r.msg.Chat.ID, fmt.Sprintf("Updated priority for job #%d to %d", jobID, priority))
}

func parseUserID(args string) (int64, string, bool) {
	fields := strings.Fields(args)
	if len(fields) < 1 {
		return 0, "", false
	}
	id, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, "", false
	}
	return id, strings.Join(fields[1:], " "), true
}

func (h *handler) cmdBan(ctx context.Context, r *request) {
	targetID, reason, ok := parseUserID(r.args)
	if !ok {
		h.reply(ctx, r.msg.Chat.ID, "Usage: /ban <user_id> [reason]")
		return
	}
	if isAdmin(h.cfg, targetID) {
		h.reply(ctx, r.msg.Chat.ID, "Admins cannot be banned.")
		return
	}
	if err := db.BanUser(ctx, h.pool, targetID, reason); err != nil {
		h.log.Error("ban user failed", "target_id", targetID, "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Failed to ban user.")
		return
	}
	h.log.Info("user banned", "target_id", targetID, "by", r.msg.From.ID, "reason", reason)
	h.reply(ctx, r.msg.Chat.ID, fmt.Sprintf("Banned user %d.", targetID))
}

func (h *handler) cmdUnban(ctx context.Context, r *request) {
	targetID, _, ok := parseUserID(r.args)
	if !ok {
		h.reply(ctx, r.msg.Chat.ID, "Usage: /unban <user_id>")
		return
	}
	removed, err := db.UnbanUser(ctx, h.pool, targetID)
	if err != nil {
		h.log.Error("unban user failed", "target_id", targetID, "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Failed to unban user.")
		return
	}
	if !removed {
		h.reply(ctx, r.msg.Chat.ID, "User was not banned.")
		return
	}
	h.log.Info("user unbanned", "target_id", targetID, "by", r.msg.From.ID)
	h.reply(ctx, r.msg.Chat.ID, fmt.Sprintf("Unbanned user %d.", targetID))
}

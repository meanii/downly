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
)

func (h *handler) commandTable() map[string]commandFunc {
	user := map[string]commandFunc{
		"start":      h.cmdStart,
		"help":       h.cmdStart,
		"queue":      h.cmdQueue,
		"history":    h.cmdHistory,
		"cancel":     h.cmdCancel,
		"mp3":        h.cmdMP3,
		"setquality": h.cmdSetQuality,
		"quality":    h.cmdQuality,
		"playlist":   h.cmdPlaylist,
		"priority": func(ctx context.Context, m *models.Message, _ string) {
			h.reply(ctx, m.Chat.ID, "Priority queue exists. Admins can use /promote <job_id> and /demote <job_id>.")
		},
	}
	admin := map[string]commandFunc{
		"stats":     h.cmdStats,
		"health":    h.cmdHealth,
		"bandwidth": h.cmdBandwidth,
		"users":     h.cmdUsers,
		"jobs":      h.cmdJobs,
		"promote":   func(ctx context.Context, m *models.Message, a string) { h.setPriority(ctx, m, a, 10) },
		"demote":    func(ctx context.Context, m *models.Message, a string) { h.setPriority(ctx, m, a, 0) },
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
	return func(ctx context.Context, m *models.Message, args string) {
		if !isAdmin(h.cfg, m.From.ID) {
			h.reply(ctx, m.Chat.ID, "Admin only command.")
			return
		}
		fn(ctx, m, args)
	}
}

func (h *handler) cmdStart(ctx context.Context, m *models.Message, _ string) {
	h.reply(ctx, m.Chat.ID, startMessage(getBotUsername(ctx, h.b)))
}

func startMessage(botUsername string) string {
	return "Send me a media URL and I will queue it for download.\n" +
		"You can send multiple URLs in one message.\n\n" +
		"Commands:\n" +
		"/start, /help - show usage\n" +
		"/queue - show your active jobs\n" +
		"/history - show past downloads\n" +
		"/mp3 <url> - extract audio only\n" +
		"/setquality - set your preferred video quality\n" +
		"/quality <url> - choose quality for one download\n" +
		"/playlist <url> [max] - download playlist (up to 25)\n" +
		"/cancel <job_id> - cancel a job\n\n" +
		"Inline mode: type @" + botUsername + " <url> in any chat.\n\n" +
		"Admin commands:\n" +
		"/stats - bot analytics\n" +
		"/health - platform health dashboard\n" +
		"/bandwidth [limit] - user bandwidth/storage report\n" +
		"/users - list all users\n" +
		"/jobs - active and pending jobs\n" +
		"/promote, /demote <job_id> - change priority\n" +
		"/broadcast <msg> - message all users\n" +
		"/ban, /unban <user_id> - block/unblock user\n\n" +
		"Repo: " + repoURL
}

func (h *handler) cmdQueue(ctx context.Context, m *models.Message, _ string) {
	jobs, err := db.GetUserJobs(ctx, h.pool, m.From.ID, 10)
	if err != nil {
		h.log.Error("list user jobs failed", "user_id", m.From.ID, "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to load your queue right now.")
		return
	}
	h.reply(ctx, m.Chat.ID, db.FormatUserQueueSummary(jobs))
}

func (h *handler) cmdHistory(ctx context.Context, m *models.Message, _ string) {
	jobs, err := db.GetUserHistory(ctx, h.pool, m.From.ID, 15)
	if err != nil {
		h.log.Error("load history failed", "user_id", m.From.ID, "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to load history.")
		return
	}
	h.reply(ctx, m.Chat.ID, db.FormatUserHistory(jobs))
}

func (h *handler) cmdCancel(ctx context.Context, m *models.Message, args string) {
	chatID, userID := m.Chat.ID, m.From.ID
	jobID, ok := parseJobID(args)
	if !ok {
		h.reply(ctx, chatID, "Usage: /cancel <job_id>")
		return
	}
	prev, err := db.CancelJob(ctx, h.pool, jobID, userID)
	if err != nil {
		h.log.Error("cancel job failed", "job_id", jobID, "error", err)
		h.reply(ctx, chatID, "Failed to cancel job right now.")
		return
	}
	switch prev {
	case db.StatusPending:
		h.reply(ctx, chatID, fmt.Sprintf("Canceled pending job #%d", jobID))
	case db.StatusProcessing:
		// Stop it right away if it runs here; a worker in another instance
		// notices the canceled status on its next heartbeat.
		h.controller.Cancel(jobID)
		h.reply(ctx, chatID, fmt.Sprintf("Canceled running job #%d", jobID))
	default:
		owns, _, err := db.OwnsJob(ctx, h.pool, jobID, userID)
		switch {
		case err != nil:
			h.log.Error("inspect job failed", "job_id", jobID, "error", err)
			h.reply(ctx, chatID, "Failed to inspect job right now.")
		case !owns:
			h.reply(ctx, chatID, "That job does not belong to you, or it does not exist.")
		default:
			h.reply(ctx, chatID, "That job has already finished.")
		}
	}
}

func (h *handler) cmdMP3(ctx context.Context, m *models.Message, args string) {
	fields := strings.Fields(args)
	if len(fields) < 1 {
		h.reply(ctx, m.Chat.ID, "Usage: /mp3 <url>")
		return
	}
	url := normalizeURL(fields[0])
	if !looksLikeURL(url) {
		h.reply(ctx, m.Chat.ID, "Invalid URL.")
		return
	}
	if !h.allowSubmit(ctx, m.Chat.ID, m.From.ID) {
		return
	}
	_, _ = h.enqueue(ctx, m.Chat.ID, m.From.ID, "audio:"+url)
}

func (h *handler) cmdQuality(ctx context.Context, m *models.Message, args string) {
	fields := strings.Fields(args)
	if len(fields) < 1 {
		h.reply(ctx, m.Chat.ID, "Usage: /quality <url>\nI will ask you to pick a resolution before downloading.")
		return
	}
	url := normalizeURL(fields[0])
	if !isQueueableURL(url) {
		h.reply(ctx, m.Chat.ID, "Invalid URL.")
		return
	}

	// The URL is too long for callback_data, so buttons carry a short token.
	token := pendingURLs.Put(url)
	var buttons []models.InlineKeyboardButton
	for _, q := range qualityOptions {
		buttons = append(buttons, models.InlineKeyboardButton{Text: q.Label, CallbackData: qualityCallbackData(q.Callback, token)})
	}
	if _, err := h.send(ctx, &bot.SendMessageParams{
		ChatID:      m.Chat.ID,
		Text:        "Pick quality for: " + trimURL(url),
		ReplyMarkup: &models.InlineKeyboardMarkup{InlineKeyboard: [][]models.InlineKeyboardButton{buttons}},
	}); err != nil {
		h.log.Warn("send quality picker failed", "error", err)
	}
}

// Quality preference options for /setquality
var qualityPreferences = []struct {
	Label string
	Value string
}{
	{"360p", "q360"},
	{"480p", "q480"},
	{"720p", "q720"},
	{"1080p", "q1080"},
	{"Best (default)", "best"},
}

func qualityPreferenceKeyboard(current string) *models.InlineKeyboardMarkup {
	var rows [][]models.InlineKeyboardButton
	for _, q := range qualityPreferences {
		label := q.Label
		if q.Value == current {
			label = "✓ " + label
		}
		rows = append(rows, []models.InlineKeyboardButton{{Text: label, CallbackData: "sq:" + q.Value}})
	}
	return &models.InlineKeyboardMarkup{InlineKeyboard: rows}
}

func qualityLabel(value string) string {
	for _, q := range qualityPreferences {
		if q.Value == value {
			return q.Label
		}
	}
	return "Best"
}

func (h *handler) cmdSetQuality(ctx context.Context, m *models.Message, _ string) {
	current, err := db.GetUserQuality(ctx, h.pool, m.From.ID)
	if err != nil {
		h.log.Warn("load quality preference failed", "user_id", m.From.ID, "error", err)
	}
	if _, err := h.send(ctx, &bot.SendMessageParams{
		ChatID:      m.Chat.ID,
		Text:        fmt.Sprintf("Current quality: %s\nTap a button to set your preferred quality.\nIf unavailable, it falls back to lower resolutions automatically.", qualityLabel(current)),
		ReplyMarkup: qualityPreferenceKeyboard(current),
	}); err != nil {
		h.log.Warn("send quality preferences failed", "error", err)
	}
}

const (
	playlistDefault = 10
	playlistMax     = 25
)

func (h *handler) cmdPlaylist(ctx context.Context, m *models.Message, args string) {
	chatID, userID := m.Chat.ID, m.From.ID
	fields := strings.Fields(args)
	if len(fields) < 1 {
		h.reply(ctx, chatID, fmt.Sprintf("Usage: /playlist <url> [max]\nFetches playlist entries and queues them for download.\nOptional: max number of videos (default %d, max %d).", playlistDefault, playlistMax))
		return
	}
	url := normalizeURL(fields[0])
	if !isQueueableURL(url) {
		h.reply(ctx, chatID, "Invalid URL.")
		return
	}
	requested := playlistDefault
	if len(fields) >= 2 {
		if n, err := strconv.Atoi(fields[1]); err == nil && n > 0 {
			requested = n
		}
	}
	requested = min(requested, playlistMax)

	if !h.allowSubmit(ctx, chatID, userID) {
		return
	}

	// Only fetch and queue what the user's limits leave room for, instead of
	// queueing until the limit trips once per remaining entry.
	room, err := db.QueueRoom(ctx, h.pool, userID, h.limitsFor(userID))
	if err != nil {
		h.log.Error("queue room check failed", "user_id", userID, "error", err)
		h.reply(ctx, chatID, "Failed to check your queue right now.")
		return
	}
	if room == 0 {
		h.reply(ctx, chatID, "Your queue is full (or you reached your daily limit). Wait for some downloads to finish, then try again.")
		return
	}
	want := requested
	if room > 0 {
		want = min(want, room)
	}

	h.reply(ctx, chatID, "Fetching playlist info... this may take a moment.")
	dl := downloader.YTDLP{Bin: h.cfg.Downly.Services.YTDLP.Bin, CookiesFile: h.cfg.Downly.Services.YTDLP.CookiesFile, Logger: h.log}
	entries, playlistTitle, err := dl.FetchPlaylist(ctx, url, want)
	if err != nil {
		h.log.Warn("fetch playlist failed", "url", url, "error", err)
		h.reply(ctx, chatID, "Failed to fetch playlist: "+truncateStr(err.Error(), 200))
		return
	}
	if len(entries) == 0 {
		h.reply(ctx, chatID, "No entries found in this playlist. It might be a single video — just send the URL directly.")
		return
	}

	summary := fmt.Sprintf("Playlist: %s\nQueueing %d entries:\n", truncateStr(playlistTitle, 80), len(entries))
	for i, e := range entries {
		summary += fmt.Sprintf("\n%d. %s", i+1, truncateStr(e.Title, 60))
	}
	if room > 0 && room < requested {
		summary += fmt.Sprintf("\n\nOnly %d of %d requested fit within your queue/daily limits.", room, requested)
	}
	h.reply(ctx, chatID, summary)

	quality := h.preferredQuality(ctx, userID)
	queued := 0
	for _, e := range entries {
		u := e.URL
		if quality != "" {
			u = quality + ":" + u
		}
		if _, err := h.enqueue(ctx, chatID, userID, u); err != nil {
			if _, isLimit := db.IsLimit(err); isLimit {
				break
			}
			continue
		}
		queued++
	}
	h.reply(ctx, chatID, fmt.Sprintf("Queued %d of %d videos from playlist.", queued, len(entries)))
}

// --- Admin ---

func (h *handler) cmdStats(ctx context.Context, m *models.Message, _ string) {
	stats, err := db.GetBotStats(ctx, h.pool)
	if err != nil {
		h.log.Error("load stats failed", "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to load stats.")
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
	h.reply(ctx, m.Chat.ID, db.FormatBotStats(stats, topUsers, topPlatforms))
}

func (h *handler) cmdHealth(ctx context.Context, m *models.Message, _ string) {
	platforms, err := db.GetPlatformHealth(ctx, h.pool, 24, 15)
	if err != nil {
		h.log.Error("load platform health failed", "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to load platform health.")
		return
	}
	h.reply(ctx, m.Chat.ID, db.FormatPlatformHealth(platforms, 24))
}

func (h *handler) cmdBandwidth(ctx context.Context, m *models.Message, args string) {
	limit := 20
	if fields := strings.Fields(args); len(fields) > 0 {
		if n, err := strconv.Atoi(fields[0]); err == nil && n > 0 {
			limit = min(n, 100)
		}
	}
	users, err := db.GetUserBandwidth(ctx, h.pool, limit)
	if err != nil {
		h.log.Error("load bandwidth failed", "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to load bandwidth data.")
		return
	}
	h.reply(ctx, m.Chat.ID, db.FormatUserBandwidth(users))
}

func (h *handler) cmdUsers(ctx context.Context, m *models.Message, _ string) {
	users, err := db.GetAllUsers(ctx, h.pool, 25)
	if err != nil {
		h.log.Error("load users failed", "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to load user list.")
		return
	}
	h.reply(ctx, m.Chat.ID, db.FormatUserList(users))
}

func (h *handler) cmdJobs(ctx context.Context, m *models.Message, _ string) {
	jobs, err := db.GetActiveJobs(ctx, h.pool, 20)
	if err != nil {
		h.log.Error("load active jobs failed", "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to load job list.")
		return
	}
	h.reply(ctx, m.Chat.ID, db.FormatActiveJobs(jobs))
}

func (h *handler) setPriority(ctx context.Context, m *models.Message, args string, priority int) {
	jobID, ok := parseJobID(args)
	if !ok {
		h.reply(ctx, m.Chat.ID, "Usage: /promote <job_id> or /demote <job_id>")
		return
	}
	updated, err := db.UpdatePriority(ctx, h.pool, jobID, priority)
	if err != nil {
		h.log.Error("update priority failed", "job_id", jobID, "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to update priority right now.")
		return
	}
	if !updated {
		h.reply(ctx, m.Chat.ID, "Only pending jobs can have priority changed.")
		return
	}
	h.reply(ctx, m.Chat.ID, fmt.Sprintf("Updated priority for job #%d to %d", jobID, priority))
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

func (h *handler) cmdBan(ctx context.Context, m *models.Message, args string) {
	targetID, reason, ok := parseUserID(args)
	if !ok {
		h.reply(ctx, m.Chat.ID, "Usage: /ban <user_id> [reason]")
		return
	}
	if isAdmin(h.cfg, targetID) {
		h.reply(ctx, m.Chat.ID, "Admins cannot be banned.")
		return
	}
	if err := db.BanUser(ctx, h.pool, targetID, reason); err != nil {
		h.log.Error("ban user failed", "target_id", targetID, "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to ban user.")
		return
	}
	h.log.Info("user banned", "target_id", targetID, "by", m.From.ID, "reason", reason)
	h.reply(ctx, m.Chat.ID, fmt.Sprintf("Banned user %d.", targetID))
}

func (h *handler) cmdUnban(ctx context.Context, m *models.Message, args string) {
	targetID, _, ok := parseUserID(args)
	if !ok {
		h.reply(ctx, m.Chat.ID, "Usage: /unban <user_id>")
		return
	}
	removed, err := db.UnbanUser(ctx, h.pool, targetID)
	if err != nil {
		h.log.Error("unban user failed", "target_id", targetID, "error", err)
		h.reply(ctx, m.Chat.ID, "Failed to unban user.")
		return
	}
	if !removed {
		h.reply(ctx, m.Chat.ID, "User was not banned.")
		return
	}
	h.log.Info("user unbanned", "target_id", targetID, "by", m.From.ID)
	h.reply(ctx, m.Chat.ID, fmt.Sprintf("Unbanned user %d.", targetID))
}

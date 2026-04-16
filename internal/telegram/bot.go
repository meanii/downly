package telegram

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/worker"
)

var (
	rateLimitMu sync.Mutex
	lastSubmit  = make(map[int64]time.Time)
)

const repoURL = "https://github.com/meanii/downly"

func RegisterHandlers(logger *slog.Logger, cfg *config.Root, controller *worker.Controller, b *bot.Bot, pool *pgxpool.Pool) {
	handlerLog := logger.With("component", "telegram")
	b.RegisterHandler(bot.HandlerTypeMessageText, "", bot.MatchTypeContains, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		if update.Message == nil || update.Message.Text == "" {
			return
		}

		text := strings.TrimSpace(update.Message.Text)
		chatID := update.Message.Chat.ID
		userID := int64(0)
		if update.Message.From != nil {
			userID = update.Message.From.ID
		}
		msgLog := handlerLog.With("chat_id", chatID, "user_id", userID)

		switch {
		case strings.HasPrefix(text, "/start"):
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: startMessage()})
			return
		case strings.HasPrefix(text, "/help"):
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: startMessage()})
			return
		case strings.HasPrefix(text, "/queue"):
			msgLog.Info("received queue command")
			jobs, err := db.GetUserJobs(ctx, pool, userID, 10)
			if err != nil {
				msgLog.Error("list user jobs failed", "error", err)
				_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to load your queue right now."})
				return
			}
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: db.FormatUserQueueSummary(jobs)})
			return
		case strings.HasPrefix(text, "/cancel"):
			msgLog.Info("received cancel command", "text", text)
			handleCancel(ctx, b, pool, controller, chatID, userID, text)
			return
		case strings.HasPrefix(text, "/promote"):
			handlePriorityUpdate(ctx, b, pool, cfg, chatID, userID, text, 10)
			return
		case strings.HasPrefix(text, "/demote"):
			handlePriorityUpdate(ctx, b, pool, cfg, chatID, userID, text, 0)
			return
		case strings.HasPrefix(text, "/stats"):
			if !isAdmin(cfg, userID) {
				_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Admin only command."})
				return
			}
			handleStats(ctx, b, pool, chatID)
			return
		case strings.HasPrefix(text, "/broadcast"):
			handleBroadcast(ctx, b, pool, cfg, chatID, userID, text)
			return
		case strings.HasPrefix(text, "/unban"):
			handleUnban(ctx, b, pool, cfg, chatID, userID, text)
			return
		case strings.HasPrefix(text, "/ban"):
			handleBan(ctx, b, pool, cfg, chatID, userID, text)
			return
		case strings.HasPrefix(text, "/history"):
			handleHistory(ctx, b, pool, chatID, userID)
			return
		case strings.HasPrefix(text, "/users"):
			handleUsers(ctx, b, pool, cfg, chatID, userID)
			return
		case strings.HasPrefix(text, "/jobs"):
			handleJobs(ctx, b, pool, cfg, chatID, userID)
			return
		case strings.HasPrefix(text, "/mp3"):
			msgLog.Info("received mp3 command")
			handleMP3(ctx, b, pool, cfg, chatID, userID, text, update)
			return
		case strings.HasPrefix(text, "/priority"):
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Priority queue exists. Admins can use /promote <job_id> and /demote <job_id>."})
			return
		case strings.HasPrefix(text, "/") || !containsURL(text):
			msgLog.Info("ignored unsupported message")
			return
		}

		// Extract all URLs from the message
		urls := extractURLs(text)
		if len(urls) == 0 {
			return
		}

		// Ban check
		banned, err := db.IsBanned(ctx, pool, userID)
		if err != nil {
			msgLog.Error("ban check failed", "error", err)
		}
		if banned {
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "You are banned from using this bot."})
			return
		}

		// Rate limit
		if !checkRateLimit(cfg, userID) {
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Slow down! Please wait before sending another URL."})
			return
		}

		for _, url := range urls {
			queueURL(ctx, b, pool, cfg, msgLog, chatID, userID, url, update)
		}
	})
}

// queueURL handles inserting a single URL job and sending the queue ack.
func queueURL(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, msgLog *slog.Logger, chatID, userID int64, url string, update *models.Update) {
	queued, processing, err := db.UserActiveCounts(ctx, pool, userID)
	if err != nil {
		msgLog.Error("user active counts failed", "error", err)
	}
	if queued >= cfg.Downly.Limits.MaxQueuedPerUser {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Queue limit reached. You already have %d pending jobs.", queued)})
		return
	}
	if processing >= cfg.Downly.Limits.MaxConcurrentPerUser {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("You already have %d running jobs. Wait a bit or cancel one with /cancel <job_id>.", processing)})
		return
	}

	priority := detectPriority(cfg, update)
	msgLog.Info("queueing url", "url", url, "priority", priority)
	reply, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Queueing your download..."})
	if err != nil {
		msgLog.Error("send queue ack failed", "error", err)
		return
	}

	jobID, err := db.InsertJob(ctx, pool, chatID, userID, url, int64(reply.ID), priority)
	if err != nil {
		msgLog.Error("insert job failed", "error", err)
		_, _ = b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: reply.ID, Text: "Failed to queue this request."})
		return
	}

	stats, err := db.GetQueueStats(ctx, pool, jobID, userID)
	if err != nil {
		msgLog.Error("get queue stats failed", "job_id", jobID, "error", err)
	}

	textOut := fmt.Sprintf("Job #%d queued\nPosition ahead: %d\nActive downloads: %d\nYour active jobs: %d", jobID, safePendingAhead(stats), safeActive(stats), safeUserPending(stats))
	if priority > 0 {
		textOut += fmt.Sprintf("\nPriority: %d", priority)
	}
	_, _ = b.EditMessageText(ctx, &bot.EditMessageTextParams{ChatID: chatID, MessageID: reply.ID, Text: textOut})
	msgLog.Info("job queued", "job_id", jobID, "telegram_message_id", reply.ID)
}

func handleMP3(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, chatID, userID int64, text string, update *models.Update) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Usage: /mp3 <url>"})
		return
	}
	url := normalizeURL(parts[1])
	if !looksLikeURL(url) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Invalid URL."})
		return
	}

	banned, _ := db.IsBanned(ctx, pool, userID)
	if banned {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "You are banned from using this bot."})
		return
	}
	if !checkRateLimit(cfg, userID) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Slow down! Please wait before sending another URL."})
		return
	}

	// Prefix with "audio:" so the worker knows to extract audio
	queueURL(ctx, b, pool, cfg, slog.Default().With("component", "telegram", "chat_id", chatID), chatID, userID, "audio:"+url, update)
}

func handleUsers(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, chatID, userID int64) {
	if !isAdmin(cfg, userID) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Admin only command."})
		return
	}
	users, err := db.GetAllUsers(ctx, pool, 25)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to load user list."})
		return
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: db.FormatUserList(users)})
}

func handleJobs(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, chatID, userID int64) {
	if !isAdmin(cfg, userID) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Admin only command."})
		return
	}
	jobs, err := db.GetActiveJobs(ctx, pool, 20)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to load job list."})
		return
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: db.FormatActiveJobs(jobs)})
}

func startMessage() string {
	return "Send me a media URL and I will queue it for download.\n" +
		"You can send multiple URLs in one message.\n\n" +
		"Commands:\n" +
		"/start, /help - show usage\n" +
		"/queue - show your active jobs\n" +
		"/history - show past downloads\n" +
		"/mp3 <url> - extract audio only\n" +
		"/cancel <job_id> - cancel a job\n\n" +
		"Admin commands:\n" +
		"/stats - bot analytics\n" +
		"/users - list all users\n" +
		"/jobs - active and pending jobs\n" +
		"/promote, /demote <job_id> - change priority\n" +
		"/broadcast <msg> - message all users\n" +
		"/ban, /unban <user_id> - block/unblock user\n\n" +
		"Repo: " + repoURL
}

func handleStats(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, chatID int64) {
	stats, err := db.GetBotStats(ctx, pool)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to load stats."})
		return
	}
	topUsers, _ := db.GetTopUsers(ctx, pool, 5)
	topPlatforms, _ := db.GetTopPlatforms(ctx, pool, 5)
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: db.FormatBotStats(stats, topUsers, topPlatforms)})
}

func handleCancel(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, controller *worker.Controller, chatID, userID int64, text string) {
	jobID, ok := parseJobID(text)
	if !ok {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Usage: /cancel <job_id>"})
		return
	}
	canceledPending, err := db.CancelPendingJob(ctx, pool, jobID, userID)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to cancel job right now."})
		return
	}
	if canceledPending {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Canceled pending job #%d", jobID)})
		return
	}
	owns, status, err := db.OwnsJob(ctx, pool, jobID, userID)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to inspect job right now."})
		return
	}
	if !owns {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "That job does not belong to you, or it does not exist."})
		return
	}
	if status != db.StatusProcessing {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "That job is not cancelable right now."})
		return
	}
	if !controller.Cancel(jobID) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Could not cancel the running job. It may have already finished."})
		return
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Cancellation requested for running job #%d", jobID)})
}

func handlePriorityUpdate(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, chatID, userID int64, text string, priority int) {
	if !isAdmin(cfg, userID) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Admin only command."})
		return
	}
	jobID, ok := parseJobID(text)
	if !ok {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Usage: /promote <job_id> or /demote <job_id>"})
		return
	}
	updated, err := db.UpdatePriority(ctx, pool, jobID, priority)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to update priority right now."})
		return
	}
	if !updated {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Only pending jobs can have priority changed."})
		return
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Updated priority for job #%d to %d", jobID, priority)})
}

func handleBroadcast(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, chatID, userID int64, text string) {
	if !isAdmin(cfg, userID) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Admin only command."})
		return
	}
	msg := strings.TrimSpace(strings.TrimPrefix(text, "/broadcast"))
	if msg == "" {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Usage: /broadcast <message>"})
		return
	}
	chatIDs, err := db.GetAllChatIDs(ctx, pool)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to fetch user list."})
		return
	}
	sent, failed := 0, 0
	for _, cid := range chatIDs {
		_, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: cid, Text: "[Broadcast] " + msg})
		if err != nil {
			failed++
		} else {
			sent++
		}
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Broadcast done. Sent: %d, Failed: %d", sent, failed)})
}

func handleBan(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, chatID, userID int64, text string) {
	if !isAdmin(cfg, userID) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Admin only command."})
		return
	}
	parts := strings.Fields(text)
	if len(parts) < 2 {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Usage: /ban <user_id> [reason]"})
		return
	}
	targetID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Invalid user ID."})
		return
	}
	reason := ""
	if len(parts) > 2 {
		reason = strings.Join(parts[2:], " ")
	}
	if err := db.BanUser(ctx, pool, targetID, reason); err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to ban user."})
		return
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Banned user %d.", targetID)})
}

func handleUnban(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, chatID, userID int64, text string) {
	if !isAdmin(cfg, userID) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Admin only command."})
		return
	}
	parts := strings.Fields(text)
	if len(parts) < 2 {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Usage: /unban <user_id>"})
		return
	}
	targetID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Invalid user ID."})
		return
	}
	removed, err := db.UnbanUser(ctx, pool, targetID)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to unban user."})
		return
	}
	if !removed {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "User was not banned."})
		return
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Unbanned user %d.", targetID)})
}

func handleHistory(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, chatID, userID int64) {
	jobs, err := db.GetUserHistory(ctx, pool, userID, 15)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to load history."})
		return
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: db.FormatUserHistory(jobs)})
}

func parseJobID(text string) (int64, bool) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		return 0, false
	}
	jobID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return jobID, true
}

func detectPriority(cfg *config.Root, update *models.Update) int {
	if update == nil || update.Message == nil || update.Message.From == nil {
		return 0
	}
	if isAdmin(cfg, update.Message.From.ID) {
		return 1
	}
	return 0
}

func isAdmin(cfg *config.Root, userID int64) bool {
	for _, id := range cfg.Downly.Admin.UserIDs {
		if id == userID {
			return true
		}
	}
	return false
}

func checkRateLimit(cfg *config.Root, userID int64) bool {
	rateLimitMu.Lock()
	defer rateLimitMu.Unlock()
	last, exists := lastSubmit[userID]
	cooldown := time.Duration(cfg.Downly.Limits.RateLimitSeconds) * time.Second
	if exists && time.Since(last) < cooldown {
		return false
	}
	lastSubmit[userID] = time.Now()
	return true
}

func safePendingAhead(stats *db.QueueStats) int {
	if stats == nil {
		return 0
	}
	return stats.PendingAhead
}

func safeActive(stats *db.QueueStats) int {
	if stats == nil {
		return 0
	}
	return stats.Active
}

func safeUserPending(stats *db.QueueStats) int {
	if stats == nil {
		return 0
	}
	return stats.UserPending
}

// extractURLs finds all URLs in a text message.
func extractURLs(text string) []string {
	var urls []string
	for _, word := range strings.Fields(text) {
		normalized := normalizeURL(word)
		if looksLikeURL(normalized) {
			urls = append(urls, normalized)
		}
	}
	return urls
}

// containsURL checks if the text contains at least one URL-like string.
func containsURL(text string) bool {
	for _, word := range strings.Fields(text) {
		if looksLikeURL(word) || looksLikeURL(normalizeURL(word)) {
			return true
		}
	}
	return false
}

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

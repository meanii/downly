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
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/worker"
)

// Quality options for inline keyboard
var qualityOptions = []struct {
	Label    string
	Callback string
}{
	{"360p", "q360"},
	{"480p", "q480"},
	{"720p", "q720"},
	{"1080p", "q1080"},
	{"Best", "qbest"},
}

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
		case strings.HasPrefix(text, "/setquality"):
			handleSetQuality(ctx, b, pool, chatID, userID)
			return
		case strings.HasPrefix(text, "/quality"):
			handleQuality(ctx, b, chatID, text)
			return
		case strings.HasPrefix(text, "/playlist"):
			msgLog.Info("received playlist command")
			handlePlaylist(ctx, b, pool, cfg, chatID, userID, text, update, msgLog)
			return
		case strings.HasPrefix(text, "/health"):
			handleHealth(ctx, b, pool, cfg, chatID, userID)
			return
		case strings.HasPrefix(text, "/priority"):
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Priority queue exists. Admins can use /promote <job_id> and /demote <job_id>."})
			return
		case strings.HasPrefix(text, "/") || !containsURL(text):
			msgLog.Info("ignored unsupported message")
			return
		}

		// Extract all URLs from the message (supports quality/audio prefixed URLs)
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
			// Apply user's quality preference if no explicit prefix
			_, prefix := stripModePrefix(url)
			if prefix == "" {
				userQuality, _ := db.GetUserQuality(ctx, pool, userID)
				if userQuality != "" && userQuality != "best" {
					url = userQuality + ":" + url
				}
			}
			queueURL(ctx, b, pool, cfg, msgLog, chatID, userID, url, update)
		}
	})

	// Callback query handler for quality selection buttons
	b.RegisterHandlerMatchFunc(func(update *models.Update) bool {
		return update.CallbackQuery != nil && strings.HasPrefix(update.CallbackQuery.Data, "dl:")
	}, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		handleQualityCallback(ctx, b, pool, cfg, handlerLog, update)
	})

	// Callback handler for /setquality preference picker
	b.RegisterHandlerMatchFunc(func(update *models.Update) bool {
		return update.CallbackQuery != nil && strings.HasPrefix(update.CallbackQuery.Data, "sq:")
	}, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		handleSetQualityCallback(ctx, b, pool, update)
	})

	// Inline query handler — lets users type @bot <url> in any chat
	b.RegisterHandlerMatchFunc(func(update *models.Update) bool {
		return update.InlineQuery != nil
	}, func(ctx context.Context, b *bot.Bot, update *models.Update) {
		handleInlineQuery(ctx, b, pool, cfg, handlerLog, update)
	})
}

// queueURL handles inserting a single URL job and sending the queue ack.
func queueURL(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, msgLog *slog.Logger, chatID, userID int64, url string, update *models.Update) {
	// Daily quota check
	if quota := cfg.Downly.Limits.DailyQuotaPerUser; quota > 0 && !isAdmin(cfg, userID) {
		dailyCount, qErr := db.UserDailyJobCount(ctx, pool, userID)
		if qErr != nil {
			msgLog.Error("daily quota check failed", "error", qErr)
		}
		if dailyCount >= quota {
			_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Daily limit reached (%d/%d). Try again tomorrow.", dailyCount, quota)})
			return
		}
	}

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
		"/setquality - set your preferred video quality\n" +
		"/quality <url> - choose quality for one download\n" +
		"/playlist <url> [max] - download playlist (up to 25)\n" +
		"/cancel <job_id> - cancel a job\n\n" +
		"Inline mode: type @bot_username <url> in any chat.\n\n" +
		"Admin commands:\n" +
		"/stats - bot analytics\n" +
		"/health - platform health dashboard\n" +
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

func handleQuality(ctx context.Context, b *bot.Bot, chatID int64, text string) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Usage: /quality <url>\nI will ask you to pick a resolution before downloading."})
		return
	}
	url := normalizeURL(parts[1])
	if !looksLikeURL(url) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Invalid URL."})
		return
	}

	// Build inline keyboard with quality buttons
	var buttons []models.InlineKeyboardButton
	for _, q := range qualityOptions {
		buttons = append(buttons, models.InlineKeyboardButton{
			Text:         q.Label,
			CallbackData: fmt.Sprintf("dl:%s:%s", q.Callback, url),
		})
	}
	keyboard := &models.InlineKeyboardMarkup{
		InlineKeyboard: [][]models.InlineKeyboardButton{buttons},
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        "Pick quality for: " + trimURL(url),
		ReplyMarkup: keyboard,
	})
}

func handleQualityCallback(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, logger *slog.Logger, update *models.Update) {
	cb := update.CallbackQuery
	if cb == nil {
		return
	}

	// Format: dl:<quality>:<url>
	data := strings.TrimPrefix(cb.Data, "dl:")
	idx := strings.Index(data, ":")
	if idx < 0 {
		return
	}
	quality := data[:idx]
	url := data[idx+1:]

	userID := cb.From.ID
	chatID := cb.From.ID // callback queries come from the user directly
	if cb.Message.Message != nil {
		chatID = cb.Message.Message.Chat.ID
	}

	_, _ = b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
		Text:            "Downloading at " + quality + "...",
	})

	// Ban check
	banned, _ := db.IsBanned(ctx, pool, userID)
	if banned {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "You are banned from using this bot."})
		return
	}
	if !checkRateLimit(cfg, userID) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Slow down! Please wait before sending another URL."})
		return
	}

	// Prefix the URL with quality marker unless "best"
	queuedURL := url
	if quality != "qbest" {
		queuedURL = quality + ":" + url
	}
	queueURL(ctx, b, pool, cfg, logger.With("chat_id", chatID), chatID, userID, queuedURL, &models.Update{})
}

func handleInlineQuery(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, logger *slog.Logger, update *models.Update) {
	iq := update.InlineQuery
	if iq == nil {
		return
	}

	query := strings.TrimSpace(iq.Query)
	if query == "" {
		// Show usage hint
		_, _ = b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
			InlineQueryID: iq.ID,
			Results: []models.InlineQueryResult{
				&models.InlineQueryResultArticle{
					ID:          "help",
					Title:       "Downly - Media Downloader",
					Description: "Paste a URL to queue a download",
					InputMessageContent: &models.InputTextMessageContent{
						MessageText: "Send a media URL to @" + getBotUsername(ctx, b) + " to download it.",
					},
				},
			},
			CacheTime:  10,
			IsPersonal: true,
		})
		return
	}

	url := normalizeURL(query)
	if !looksLikeURL(url) {
		return
	}

	userID := iq.From.ID

	// Ban check
	banned, _ := db.IsBanned(ctx, pool, userID)
	if banned {
		return
	}

	// Build results: download options
	results := []models.InlineQueryResult{
		&models.InlineQueryResultArticle{
			ID:          "dl_best",
			Title:       "Download (best quality)",
			Description: trimURL(url),
			InputMessageContent: &models.InputTextMessageContent{
				MessageText: url,
			},
		},
		&models.InlineQueryResultArticle{
			ID:          "dl_720",
			Title:       "Download (720p)",
			Description: trimURL(url),
			InputMessageContent: &models.InputTextMessageContent{
				MessageText: "q720:" + url,
			},
		},
		&models.InlineQueryResultArticle{
			ID:          "dl_480",
			Title:       "Download (480p)",
			Description: trimURL(url),
			InputMessageContent: &models.InputTextMessageContent{
				MessageText: "q480:" + url,
			},
		},
		&models.InlineQueryResultArticle{
			ID:          "dl_mp3",
			Title:       "Download (audio only)",
			Description: trimURL(url),
			InputMessageContent: &models.InputTextMessageContent{
				MessageText: "audio:" + url,
			},
		},
	}

	_, _ = b.AnswerInlineQuery(ctx, &bot.AnswerInlineQueryParams{
		InlineQueryID: iq.ID,
		Results:       results,
		CacheTime:     5,
		IsPersonal:    true,
	})
}

func handlePlaylist(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, chatID, userID int64, text string, update *models.Update, msgLog *slog.Logger) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Usage: /playlist <url> [max]\nFetches playlist entries and queues them for download.\nOptional: max number of videos (default 10, max 25)."})
		return
	}
	url := normalizeURL(parts[1])
	if !looksLikeURL(url) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Invalid URL."})
		return
	}

	maxItems := 10
	if len(parts) >= 3 {
		if n, err := strconv.Atoi(parts[2]); err == nil && n > 0 {
			maxItems = n
		}
	}
	if maxItems > 25 {
		maxItems = 25
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

	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Fetching playlist info... this may take a moment."})

	dl := downloader.YTDLP{Bin: cfg.Downly.Services.YTDLP.Bin, CookiesFile: cfg.Downly.Services.YTDLP.CookiesFile}
	entries, playlistTitle, err := dl.FetchPlaylist(ctx, url, maxItems)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to fetch playlist: " + truncateStr(err.Error(), 200)})
		return
	}
	if len(entries) == 0 {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "No entries found in this playlist. It might be a single video — just send the URL directly."})
		return
	}

	// Build summary message
	summary := fmt.Sprintf("Playlist: %s\nFound %d entries (showing first %d):\n", truncateStr(playlistTitle, 80), len(entries), len(entries))
	for i, e := range entries {
		summary += fmt.Sprintf("\n%d. %s", i+1, truncateStr(e.Title, 60))
	}
	summary += fmt.Sprintf("\n\nQueueing %d downloads...", len(entries))
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: summary})

	// Queue each entry
	queued := 0
	for _, e := range entries {
		if e.URL == "" {
			continue
		}
		// Check daily quota before each queue
		if quota := cfg.Downly.Limits.DailyQuotaPerUser; quota > 0 && !isAdmin(cfg, userID) {
			dailyCount, _ := db.UserDailyJobCount(ctx, pool, userID)
			if dailyCount >= quota {
				_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Daily limit reached after %d videos. Remaining items skipped.", queued)})
				break
			}
		}
		queueURL(ctx, b, pool, cfg, msgLog, chatID, userID, e.URL, update)
		queued++
	}
	if queued > 0 {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: fmt.Sprintf("Queued %d videos from playlist.", queued)})
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

func handleSetQuality(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, chatID, userID int64) {
	current, _ := db.GetUserQuality(ctx, pool, userID)
	currentLabel := "Best"
	for _, q := range qualityPreferences {
		if q.Value == current {
			currentLabel = q.Label
		}
	}

	// Build inline keyboard with quality buttons (one per row)
	var rows [][]models.InlineKeyboardButton
	for _, q := range qualityPreferences {
		label := q.Label
		if q.Value == current {
			label = "✓ " + label
		}
		rows = append(rows, []models.InlineKeyboardButton{{
			Text:         label,
			CallbackData: "sq:" + q.Value,
		}})
	}
	keyboard := &models.InlineKeyboardMarkup{
		InlineKeyboard: rows,
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:      chatID,
		Text:        fmt.Sprintf("Current quality: %s\nTap a button to set your preferred quality.\nIf unavailable, it falls back to lower resolutions automatically.", currentLabel),
		ReplyMarkup: keyboard,
	})
}

func handleSetQualityCallback(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, update *models.Update) {
	cb := update.CallbackQuery
	if cb == nil {
		return
	}

	quality := strings.TrimPrefix(cb.Data, "sq:")
	userID := cb.From.ID
	chatID := cb.From.ID
	if cb.Message.Message != nil {
		chatID = cb.Message.Message.Chat.ID
	}

	if err := db.SetUserQuality(ctx, pool, userID, quality); err != nil {
		_, _ = b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
			CallbackQueryID: cb.ID,
			Text:            "Failed to save preference.",
		})
		return
	}

	label := quality
	for _, q := range qualityPreferences {
		if q.Value == quality {
			label = q.Label
		}
	}

	_, _ = b.AnswerCallbackQuery(ctx, &bot.AnswerCallbackQueryParams{
		CallbackQueryID: cb.ID,
		Text:            "Quality set to " + label,
	})

	// Update the original message to show the new selection
	current := quality
	var newRows [][]models.InlineKeyboardButton
	for _, q := range qualityPreferences {
		btnLabel := q.Label
		if q.Value == current {
			btnLabel = "✓ " + btnLabel
		}
		newRows = append(newRows, []models.InlineKeyboardButton{{
			Text:         btnLabel,
			CallbackData: "sq:" + q.Value,
		}})
	}
	if cb.Message.Message != nil {
		_, _ = b.EditMessageText(ctx, &bot.EditMessageTextParams{
			ChatID:    chatID,
			MessageID: cb.Message.Message.ID,
			Text:      fmt.Sprintf("Quality preference saved: %s\nAll your downloads will now use this setting. If unavailable, it falls back to lower resolutions automatically.", label),
			ReplyMarkup: &models.InlineKeyboardMarkup{
				InlineKeyboard: newRows,
			},
		})
	}
}

func handleHealth(ctx context.Context, b *bot.Bot, pool *pgxpool.Pool, cfg *config.Root, chatID, userID int64) {
	if !isAdmin(cfg, userID) {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Admin only command."})
		return
	}
	platforms, err := db.GetPlatformHealth(ctx, pool, 24, 15)
	if err != nil {
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: "Failed to load platform health."})
		return
	}
	_, _ = b.SendMessage(ctx, &bot.SendMessageParams{ChatID: chatID, Text: db.FormatPlatformHealth(platforms, 24)})
}

func truncateStr(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}

var cachedBotUsername string

func getBotUsername(ctx context.Context, b *bot.Bot) string {
	if cachedBotUsername != "" {
		return cachedBotUsername
	}
	me, err := b.GetMe(ctx)
	if err == nil && me.Username != "" {
		cachedBotUsername = me.Username
	}
	return cachedBotUsername
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

// stripModePrefix removes quality/audio prefixes from a word, returning the clean word and the prefix.
func stripModePrefix(word string) (clean, prefix string) {
	for _, p := range []string{"audio:", "q360:", "q480:", "q720:", "q1080:"} {
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
	for _, word := range strings.Fields(text) {
		clean, _ := stripModePrefix(word)
		if looksLikeURL(clean) || looksLikeURL(normalizeURL(clean)) {
			return true
		}
	}
	return false
}

func trimURL(s string) string {
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
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

package telegram

import (
	"context"
	"errors"

	"github.com/go-telegram/bot"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/i18n"
	"github.com/meanii/downly/internal/media"
	"github.com/meanii/downly/internal/safeurl"
	"github.com/meanii/downly/internal/worker"
)

// download is one request to fetch a URL for a chat.
type download struct {
	chatID, userID int64
	lang           i18n.Lang
	// url may carry a mode prefix ("audio:", "q720:", ...).
	url string
	// inlineMessageID is set when the request came from inline mode.
	inlineMessageID string
	// replyTo is the message to answer (0 = none).
	replyTo int
}

func (d download) sendOptions(caption string) worker.SendOptions {
	return worker.SendOptions{Caption: caption, ReplyTo: d.replyTo}
}

// enqueue validates the URL, serves it from the media cache when possible,
// and otherwise inserts a job within the user's limits and posts the status
// message the worker keeps updated. Every failure is reported to the user.
func (h *handler) enqueue(ctx context.Context, d download) (int64, error) {
	log := h.log.With("chat_id", d.chatID, "user_id", d.userID)
	if !isQueueableURL(d.url) {
		log.Warn("rejected unsafe url", "url", d.url)
		h.replyTo(ctx, d.chatID, d.replyTo, i18n.T(d.lang, "url_unsupported"))
		return 0, safeurl.ErrInvalidURL
	}
	cleanURL, mode, quality := jobSpec(d.url)
	job := db.NewJob{
		ChatID: d.chatID, UserID: d.userID, URL: cleanURL, Mode: mode, Quality: quality,
		CacheKey:        media.CacheKey(cleanURL, string(mode), quality, ""),
		InlineMessageID: d.inlineMessageID,
		ReplyTo:         int64(d.replyTo),
	}

	if id, ok := h.serveFromCache(ctx, d, job); ok {
		return id, nil
	}

	if isAdmin(h.cfg, d.userID) {
		job.Priority = 1
	}
	reply, err := h.send(ctx, &bot.SendMessageParams{ChatID: d.chatID, Text: i18n.T(d.lang, "queueing"), ReplyParameters: replyParams(d.replyTo)})
	if err != nil {
		log.Error("send queue ack failed", "error", err)
		return 0, err
	}
	job.TelegramMsgID = int64(reply.ID)

	jobID, err := db.EnqueueJob(ctx, h.pool, job, h.limitsFor(d.userID))
	if le, ok := db.IsLimit(err); ok {
		h.edit(ctx, d.chatID, reply.ID, limitMessage(d.lang, le))
		return 0, err
	}
	if err != nil {
		log.Error("insert job failed", "error", err)
		h.edit(ctx, d.chatID, reply.ID, i18n.T(d.lang, "queue_failed"))
		return 0, err
	}

	stats, err := db.GetQueueStats(ctx, h.pool, jobID, d.userID)
	if err != nil {
		log.Error("get queue stats failed", "job_id", jobID, "error", err)
		stats = &db.QueueStats{}
	}
	textOut := i18n.T(d.lang, "queued", jobID, stats.PendingAhead, stats.Active, stats.UserPending)
	if job.Priority > 0 {
		textOut += "\n" + i18n.T(d.lang, "priority_line", job.Priority)
	}
	h.edit(ctx, d.chatID, reply.ID, textOut)
	log.Info("job queued", "job_id", jobID, "url", cleanURL, "mode", mode, "quality", quality, "telegram_message_id", reply.ID)
	return jobID, nil
}

// serveFromCache delivers job from the media cache. ok is false on a miss
// or if Telegram rejected the cached file, in which case the caller should
// download normally.
func (h *handler) serveFromCache(ctx context.Context, d download, job db.NewJob) (int64, bool) {
	if h.cfg.Downly.Cache.Disabled || job.CacheKey == "" {
		return 0, false
	}
	entry, ok, err := db.GetCache(ctx, h.pool, job.CacheKey)
	if err != nil {
		h.log.Warn("media cache lookup failed", "error", err)
		return 0, false
	}
	if !ok {
		return 0, false
	}
	if err := h.deliverCached(ctx, d, entry); err != nil {
		return 0, false
	}
	id, err := db.InsertCachedJob(ctx, h.pool, job, entry)
	if err != nil {
		h.log.Warn("record cached delivery failed", "error", err)
	}
	h.log.Info("served from cache", "job_id", id, "chat_id", d.chatID, "url", job.URL)
	return id, true
}

// deliverCached sends a cache entry to the request's chat (or inline
// message). A file ID Telegram rejects is dropped from the cache.
func (h *handler) deliverCached(ctx context.Context, d download, entry *db.CacheEntry) error {
	caption := worker.Caption(d.lang, entry.Meta)
	var err error
	if d.inlineMessageID != "" && len(entry.Items) == 1 {
		err = h.msgr.EditInlineMedia(ctx, d.inlineMessageID, entry.Items[0], caption)
	} else {
		err = h.msgr.SendCached(ctx, d.chatID, entry.Items, entry.Meta, d.sendOptions(caption))
	}
	if err != nil {
		h.log.Warn("cached delivery failed", "cache_key", entry.Key, "error", err)
		if errors.Is(err, bot.ErrorBadRequest) {
			_ = db.DeleteCache(ctx, h.pool, entry.Key)
		}
		return err
	}
	if err := db.TouchCache(ctx, h.pool, entry.Key); err != nil {
		h.log.Warn("touch media cache failed", "error", err)
	}
	return nil
}

// jobPrefix rebuilds the URL prefix that recreates a job's mode and quality.
func jobPrefix(job *db.Job) string {
	if job.Mode == db.ModeAudio {
		return "audio:"
	}
	if job.Quality != "" {
		return job.Quality + ":"
	}
	return ""
}

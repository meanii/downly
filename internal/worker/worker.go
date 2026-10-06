package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/health"
	"github.com/meanii/downly/internal/i18n"
	"github.com/meanii/downly/internal/media"
	"github.com/meanii/downly/internal/tgutil"
)

var (
	errCanceledByUser = errors.New("canceled by user")
	// errJobLost means the job left "processing" behind our back: it was
	// canceled from another instance or reaped.
	errJobLost = errors.New("job no longer owned by this worker")
)

// finalizeTimeout bounds the DB/Telegram writes made after a job ends. They
// run on a context detached from shutdown so a stopping worker can still
// record what happened.
const finalizeTimeout = 20 * time.Second

// Worker claims and processes jobs one at a time.
type Worker struct {
	ID         string
	Cfg        *config.Root
	Pool       *pgxpool.Pool
	DL         Downloader
	Msg        Messenger
	Controller *Controller
	Waker      *Waker
	Log        *slog.Logger

	// Health receives liveness pings and outcome counters; optional.
	Health *health.Registry

	// HeartbeatInterval defaults to 30s; tests shorten it.
	HeartbeatInterval time.Duration

	// AfterJob, if set, is called once a job's processing has fully ended
	// (all messages sent, files removed). Used by tests.
	AfterJob func(jobID int64)
}

func (w *Worker) seen() {
	if w.Health != nil {
		w.Health.WorkerSeen(w.ID)
	}
}

// count records a job outcome (done, failed, retried, canceled, requeued).
func (w *Worker) count(result string) {
	if w.Health != nil {
		w.Health.Inc(`downly_jobs_finished_total{result="` + result + `"}`)
	}
}

func (w *Worker) heartbeatInterval() time.Duration {
	if w.HeartbeatInterval > 0 {
		return w.HeartbeatInterval
	}
	return 30 * time.Second
}

func (w *Worker) jobTimeout() time.Duration {
	return time.Duration(w.Cfg.Downly.Worker.JobTimeoutMinutes) * time.Minute
}

// Run claims jobs until claimCtx ends. Jobs already running continue until
// they finish or workCtx ends, in which case they are requeued.
func (w *Worker) Run(claimCtx, workCtx context.Context) {
	log := w.Log.With("component", "worker", "worker_id", w.ID)
	poll := time.Duration(w.Cfg.Downly.Worker.PollIntervalSec) * time.Second
	log.Info("worker started", "poll_interval", poll.String())
	defer log.Info("worker stopped")
	if w.Health != nil {
		defer w.Health.WorkerGone(w.ID)
	}

	for claimCtx.Err() == nil {
		w.seen()
		var wake <-chan struct{}
		if w.Waker != nil {
			wake = w.Waker.C()
		}
		job, err := db.ClaimJobLimited(claimCtx, w.Pool, w.ID, w.Cfg.Downly.Limits.MaxConcurrentPerUser)
		if err != nil {
			if claimCtx.Err() == nil {
				log.Error("claim job failed", "error", err)
			}
			sleep(claimCtx, poll, nil)
			continue
		}
		if job == nil {
			sleep(claimCtx, poll, wake)
			continue
		}
		w.process(workCtx, log, job)
	}
}

// sleep waits for d, a wake-up, or ctx to end.
func sleep(ctx context.Context, d time.Duration, wake <-chan struct{}) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	case <-wake:
	}
}

func (w *Worker) process(workCtx context.Context, workerLog *slog.Logger, job *db.Job) {
	log := workerLog.With("job_id", job.ID, "chat_id", job.ChatID, "url", job.URL, "mode", job.Mode, "quality", job.Quality)
	log.Info("job claimed", "retry_count", job.RetryCount)
	if w.AfterJob != nil {
		defer w.AfterJob(job.ID)
	}
	workDir := w.Cfg.Downly.Worker.WorkDir
	defer func() { _ = os.RemoveAll(jobDir(workDir, job.ID)) }()

	runCtx, cancelRun := context.WithCancelCause(workCtx)
	defer cancelRun(nil)
	w.Controller.Register(job.ID, func() { cancelRun(errCanceledByUser) })
	defer w.Controller.Unregister(job.ID)
	go w.heartbeat(runCtx, log, job.ID, cancelRun)

	lang := w.jobLang(runCtx, log, job)
	msgID := int(job.TelegramMsgID)
	progress := newProgressReporter(runCtx,
		func(ctx context.Context, text string, percent int) error {
			return db.UpdateProgress(ctx, w.Pool, job.ID, text, percent)
		},
		func(ctx context.Context, text string) error { return w.Msg.Edit(ctx, job.ChatID, msgID, text) },
		func(text string, percent int) string {
			return formatProgressMessage(lang, job.ID, job.Status, text, percent, 0, 1, job.Priority)
		},
	)
	progress.quiet = isGroupChat(job.ChatID)
	if w.serveCached(runCtx, workCtx, log, job, lang) {
		return
	}
	progress.Force(job.ProgressText, job.ProgressPercent)

	dlCtx, cancelDL := context.WithTimeout(runCtx, w.jobTimeout())
	res, err := w.download(dlCtx, log, job, lang, progress)
	timedOut := errors.Is(dlCtx.Err(), context.DeadlineExceeded) && runCtx.Err() == nil
	cancelDL()

	if err == nil || runCtx.Err() != nil {
		if w.handleInterrupted(workCtx, runCtx, log, job, lang) {
			return
		}
	}
	if err != nil {
		if timedOut {
			err = fmt.Errorf("timed out after %s", w.jobTimeout())
		}
		w.fail(workCtx, log, job, lang, err, isPermanent(err))
		return
	}

	size, tooBig, err := w.checkSizes(log, res)
	if err != nil {
		w.fail(workCtx, log, job, lang, err, false)
		return
	}
	log.Info("download completed", "platform", res.Platform, "file_name", res.FileName, "size_bytes", size, "media_type", res.Media, "items", 1+len(res.More))
	if tooBig > 0 {
		msg := i18n.T(lang, "too_large", float64(tooBig)/1024/1024, w.Cfg.Downly.Worker.MaxFileSizeMB)
		w.fail(workCtx, log, job, lang, errors.New(msg), true)
		return
	}

	progress.Force("Uploading to Telegram", 99)
	meta := MetaFromResult(res, size)
	items, err := uploadWithRetry(runCtx, w.Msg, job.ChatID, res, SendOptions{Caption: Caption(lang, meta), ReplyTo: int(job.ReplyTo)}, size)
	if runCtx.Err() != nil && w.handleInterrupted(workCtx, runCtx, log, job, lang) {
		return
	}
	if err != nil {
		if errors.Is(err, errForbidden) {
			fctx, cancel := w.finalizeCtx(workCtx)
			_ = db.MarkUserBlocked(fctx, w.Pool, job.ChatID)
			cancel()
		}
		if w.Health != nil {
			w.Health.Inc("downly_upload_failures_total")
		}
		w.fail(workCtx, log, job, lang, fmt.Errorf("upload failed: %w", err), isPermanentUploadErr(err))
		return
	}

	fctx, cancel := w.finalizeCtx(workCtx)
	defer cancel()
	// Cache first: once the job reads as done, a repeat request must hit.
	w.remember(fctx, log, job, items, meta)
	if err := db.MarkDone(fctx, w.Pool, job.ID, res.FilePath, res.FileName, res.Platform, size); err != nil {
		log.Error("mark done failed", "error", err)
	}
	w.finishInline(fctx, log, job, lang, items, meta)
	if isGroupChat(job.ChatID) {
		// The media itself answers the link; the status message is clutter.
		w.deleteStatus(fctx, log, job)
	} else {
		w.editFinal(fctx, job, formatDoneMessage(lang, job.ID, res))
	}
	w.count("done")
	log.Info("job finished", "platform", res.Platform, "file_name", res.FileName)
}

func (w *Worker) download(ctx context.Context, log *slog.Logger, job *db.Job, lang i18n.Lang, progress *progressReporter) (*downloader.Result, error) {
	workDir := w.Cfg.Downly.Worker.WorkDir
	clip := downloader.Range{Start: job.ClipStart, End: job.ClipEnd}
	hasClip := job.ClipEnd > job.ClipStart
	switch {
	case job.Mode == db.ModeGIF:
		var r *downloader.Range
		if hasClip {
			r = &clip
		}
		return w.DL.DownloadGIF(ctx, workDir, job.ID, job.URL, r, progress.Update)
	case hasClip:
		return w.DL.DownloadClip(ctx, workDir, job.ID, job.URL, job.Quality, clip, progress.Update)
	case job.Mode == db.ModeAudio:
		return w.DL.DownloadAudio(ctx, workDir, job.ID, job.URL, progress.Update)
	case job.Quality != "":
		// Cascading quality fallback: try preferred, then step down.
		chain := downloader.QualityFallbackChain(job.Quality)
		var res *downloader.Result
		var err error
		for i, q := range chain {
			if i > 0 {
				log.Warn("quality fallback", "from", chain[i-1], "to", q, "error", err)
				progress.Force(i18n.T(lang, "stage_fallback", chain[i-1], q), 5)
				cleanJobDir(workDir, job.ID)
			}
			if q == "best" {
				res, err = w.DL.Download(ctx, workDir, job.ID, job.URL, progress.Update)
			} else {
				res, err = w.DL.DownloadWithQuality(ctx, workDir, job.ID, job.URL, q, progress.Update)
			}
			if err == nil || ctx.Err() != nil || isPermanent(err) {
				break
			}
		}
		return res, err
	default:
		return w.DL.Download(ctx, workDir, job.ID, job.URL, progress.Update)
	}
}

// handleInterrupted deals with a job whose run context ended early. It
// returns false if the run context is still live (nothing to do).
func (w *Worker) handleInterrupted(workCtx, runCtx context.Context, log *slog.Logger, job *db.Job, lang i18n.Lang) bool {
	if runCtx.Err() == nil {
		return false
	}
	fctx, cancel := w.finalizeCtx(workCtx)
	defer cancel()
	switch cause := context.Cause(runCtx); {
	case workCtx.Err() != nil:
		log.Warn("job interrupted by shutdown, requeueing")
		if err := db.RequeueJob(fctx, w.Pool, job.ID, "Queued (bot restarted)"); err != nil {
			log.Error("requeue failed", "error", err)
			return true
		}
		w.editFinal(fctx, job, i18n.T(lang, "job_header", job.ID)+"\n"+i18n.T(lang, "restarting"))
		w.count("requeued")
	case errors.Is(cause, errCanceledByUser):
		log.Info("job canceled by user")
		// /cancel already marked the row; this is a no-op unless it raced.
		_ = db.MarkCanceled(fctx, w.Pool, job.ID, "Canceled by user")
		w.editFinal(fctx, job, formatCanceledMessage(lang, job.ID))
		w.count("canceled")
	case errors.Is(cause, errJobLost):
		st, _ := db.GetJobStatus(fctx, w.Pool, job.ID)
		log.Warn("job taken away from worker", "status", st)
		if st == db.StatusCanceled {
			w.editFinal(fctx, job, formatCanceledMessage(lang, job.ID))
		}
	default:
		log.Warn("job context ended", "cause", cause)
	}
	return true
}

// fail records a failed attempt, scheduling a retry when it may help.
func (w *Worker) fail(workCtx context.Context, log *slog.Logger, job *db.Job, lang i18n.Lang, err error, permanent bool) {
	fctx, cancel := w.finalizeCtx(workCtx)
	defer cancel()
	if dbErr := db.SetPlatformIfEmpty(fctx, w.Pool, job.ID, media.PlatformFromURL(job.URL)); dbErr != nil {
		log.Warn("record platform failed", "error", dbErr)
	}
	userMsg := friendlyError(err)
	if downloader.IsCrash(err) {
		// A Python traceback means nothing to users.
		userMsg = i18n.T(lang, "error_internal")
	}
	if !permanent && job.RetryCount < w.Cfg.Downly.Limits.MaxRetries {
		delay := retryDelay(job.RetryCount)
		log.Warn("download failed, scheduling retry", "error", err, "retry_count", job.RetryCount+1, "delay", delay.String())
		if dbErr := db.MarkFailedForRetry(fctx, w.Pool, job.ID, truncate(err.Error()), delay); dbErr != nil {
			log.Error("schedule retry failed", "error", dbErr)
			return
		}
		w.editFinal(fctx, job, i18n.T(lang, "job_header", job.ID)+"\n"+i18n.T(lang, "retrying", userMsg, delay.Round(time.Second).String()))
		w.count("retried")
		return
	}
	log.Error("download failed", "error", err, "permanent", permanent, "retry_count", job.RetryCount)
	if dbErr := db.MarkFailed(fctx, w.Pool, job.ID, truncate(err.Error())); dbErr != nil {
		log.Error("mark failed failed", "error", dbErr)
	}
	if isGroupChat(job.ChatID) && isNotMedia(err) {
		// Groups post plenty of ordinary links; don't answer them with errors.
		w.deleteStatus(fctx, log, job)
		w.count("failed")
		return
	}
	w.editFinal(fctx, job, formatFailureMessage(lang, job.ID, userMsg))
	if job.InlineMessageID != "" {
		if err := w.Msg.EditInlineText(fctx, job.InlineMessageID, "❌ "+userMsg); err != nil {
			log.Warn("edit inline message failed", "error", err)
		}
	}
	w.count("failed")
}

// heartbeat keeps the job alive in the DB and stops the run if the job was
// canceled or reaped elsewhere.
func (w *Worker) heartbeat(ctx context.Context, log *slog.Logger, jobID int64, cancel context.CancelCauseFunc) {
	t := time.NewTicker(w.heartbeatInterval())
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.seen()
			owned, err := db.Heartbeat(ctx, w.Pool, jobID)
			if err != nil {
				if ctx.Err() == nil {
					log.Warn("heartbeat failed", "error", err)
				}
				continue
			}
			if !owned {
				cancel(errJobLost)
				return
			}
		}
	}
}

func (w *Worker) finalizeCtx(workCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(workCtx), finalizeTimeout)
}

// editFinal shows a final status, waiting out a 429 rather than dropping it.
func (w *Worker) editFinal(ctx context.Context, job *db.Job, text string) {
	_ = tgutil.Call(ctx, 3, func() error { return w.Msg.Edit(ctx, job.ChatID, int(job.TelegramMsgID), text) })
}

// checkSizes totals the result's files against the upload limit. Album
// extras that are too large are dropped; if the primary file is, tooBig is
// its size and the job should fail.
func (w *Worker) checkSizes(log *slog.Logger, res *downloader.Result) (total, tooBig int64, err error) {
	limit := w.Cfg.Downly.Worker.MaxFileSizeMB * 1024 * 1024
	fi, err := os.Stat(res.FilePath)
	if err != nil {
		return 0, 0, err
	}
	if fi.Size() > limit {
		return fi.Size(), fi.Size(), nil
	}
	total = fi.Size()
	kept := res.More[:0]
	for _, it := range res.More {
		st, err := os.Stat(it.FilePath)
		if err != nil || st.Size() > limit {
			log.Warn("dropping album item", "file", it.FileName, "error", err)
			continue
		}
		total += st.Size()
		kept = append(kept, it)
	}
	res.More = kept
	return total, 0, nil
}

// isGroupChat reports whether chatID is a group or supergroup (Telegram
// gives those negative IDs).
func isGroupChat(chatID int64) bool { return chatID < 0 }

func (w *Worker) deleteStatus(ctx context.Context, log *slog.Logger, job *db.Job) {
	if err := w.Msg.Delete(ctx, job.ChatID, int(job.TelegramMsgID)); err != nil {
		log.Warn("delete status message failed", "error", err)
	}
}

// serveCached delivers a job straight from the media cache when the same
// content was sent before (jobs from subscriptions, retries or other
// instances reach here without the handler's cache check). It reports
// whether the job is finished.
func (w *Worker) serveCached(runCtx, workCtx context.Context, log *slog.Logger, job *db.Job, lang i18n.Lang) bool {
	if job.CacheKey == "" || w.Cfg.Downly.Cache.Disabled {
		return false
	}
	e, ok, err := db.GetCache(runCtx, w.Pool, job.CacheKey)
	if err != nil || !ok {
		return false
	}
	caption := Caption(lang, e.Meta)
	if err := w.Msg.SendCached(runCtx, job.ChatID, e.Items, e.Meta, SendOptions{Caption: caption, ReplyTo: int(job.ReplyTo)}); err != nil {
		log.Warn("cached delivery failed, downloading instead", "error", err)
		if errors.Is(err, bot.ErrorBadRequest) {
			_ = db.DeleteCache(runCtx, w.Pool, e.Key)
		}
		return false
	}
	fctx, cancel := w.finalizeCtx(workCtx)
	defer cancel()
	_ = db.TouchCache(fctx, w.Pool, e.Key)
	if err := db.MarkDone(fctx, w.Pool, job.ID, "", e.Title, e.Platform, e.SizeBytes); err != nil {
		log.Error("mark done failed", "error", err)
	}
	w.finishInline(fctx, log, job, lang, e.Items, e.Meta)
	if isGroupChat(job.ChatID) {
		w.deleteStatus(fctx, log, job)
	} else {
		w.editFinal(fctx, job, formatDoneMessage(lang, job.ID, &downloader.Result{Platform: e.Platform, Title: e.Title}))
	}
	w.count("done")
	log.Info("job served from cache")
	return true
}

// remember stores the uploaded file IDs so the same request is served
// instantly next time.
func (w *Worker) remember(ctx context.Context, log *slog.Logger, job *db.Job, items []media.Item, meta media.Meta) {
	if job.CacheKey == "" || len(items) == 0 || w.Cfg.Downly.Cache.Disabled {
		return
	}
	if err := db.PutCache(ctx, w.Pool, &db.CacheEntry{Key: job.CacheKey, Items: items, Meta: meta}); err != nil {
		log.Warn("store media cache failed", "error", err)
	}
}

// finishInline swaps an inline-mode placeholder for the downloaded media.
// Telegram cannot upload into inline messages, so this reuses the file ID
// from the copy just sent to the user's private chat.
func (w *Worker) finishInline(ctx context.Context, log *slog.Logger, job *db.Job, lang i18n.Lang, items []media.Item, meta media.Meta) {
	if job.InlineMessageID == "" || len(items) == 0 {
		return
	}
	if err := w.Msg.EditInlineMedia(ctx, job.InlineMessageID, items[0], Caption(lang, meta)); err != nil {
		log.Warn("edit inline media failed", "error", err)
	}
}

// jobLang picks the language for a job's messages: the chat's setting, then
// the requesting user's own setting, then the default.
func (w *Worker) jobLang(ctx context.Context, log *slog.Logger, job *db.Job) i18n.Lang {
	for _, id := range []int64{job.ChatID, job.UserID} {
		if id == 0 {
			continue
		}
		code, ok, err := db.GetChatLanguage(ctx, w.Pool, id)
		if err != nil {
			log.Warn("load chat language failed", "chat_id", id, "error", err)
			break
		}
		if l, valid := i18n.Parse(code); ok && valid {
			return l
		}
	}
	return i18n.Default
}

// SweepWorkDir removes job directories left behind by a crash.
func SweepWorkDir(log *slog.Logger, workDir string, olderThan time.Duration) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "job-") {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < olderThan {
			continue
		}
		p := filepath.Join(workDir, e.Name())
		if err := os.RemoveAll(p); err == nil {
			log.Info("removed stale job dir", "path", p)
		}
	}
}

func jobDir(workDir string, jobID int64) string {
	return filepath.Join(workDir, fmt.Sprintf("job-%d", jobID))
}

// cleanJobDir removes all files in a job directory before a fallback attempt.
func cleanJobDir(workDir string, jobID int64) {
	dir := jobDir(workDir, jobID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func formatProgressMessage(lang i18n.Lang, jobID int64, status db.JobStatus, progress string, percent, ahead, active, priority int) string {
	msg := i18n.T(lang, "job_header", jobID) + "\n" + i18n.T(lang, "status_line", i18n.Status(lang, string(status)))
	if priority > 0 {
		msg += "\n" + i18n.T(lang, "priority_line", priority)
	}
	if ahead > 0 {
		msg += "\n" + i18n.T(lang, "queue_position", ahead+1)
	}
	if active > 0 {
		msg += "\n" + i18n.T(lang, "active_workers", active)
	}
	if progress != "" {
		bar := progressBar(percent)
		msg += fmt.Sprintf("\n%s %d%%\n%s", bar, percent, i18n.Stage(lang, progress))
	}
	return msg
}

func progressBar(percent int) string {
	total := 20
	filled := percent * total / 100
	if filled < 0 {
		filled = 0
	}
	if filled > total {
		filled = total
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", total-filled) + "]"
}

func formatFailureMessage(lang i18n.Lang, jobID int64, err string) string {
	return i18n.T(lang, "job_header", jobID) + "\n" +
		i18n.T(lang, "status_line", i18n.Status(lang, string(db.StatusFailed))) + "\n" +
		i18n.T(lang, "error_line", err)
}

func formatCanceledMessage(lang i18n.Lang, jobID int64) string {
	return i18n.T(lang, "job_header", jobID) + "\n" +
		i18n.T(lang, "status_line", i18n.Status(lang, string(db.StatusCanceled))) + "\n" +
		i18n.T(lang, "reason_line", i18n.T(lang, "canceled_by_user"))
}

func formatDoneMessage(lang i18n.Lang, jobID int64, res *downloader.Result) string {
	msg := i18n.T(lang, "job_header", jobID) + "\n" + i18n.T(lang, "status_line", i18n.Status(lang, string(db.StatusDone)))
	if res.Platform != "" && res.Platform != "unknown" {
		msg += "\n" + i18n.T(lang, "caption_source", res.Platform)
	}
	if res.Title != "" {
		msg += "\n" + truncateRunes(res.Title, 80)
	}
	return msg
}

func truncate(s string) string {
	return truncateRunes(s, 300)
}

// truncateRunes shortens s to at most max runes without splitting a UTF-8
// sequence (Telegram rejects invalid UTF-8), adding "..." when cut.
func truncateRunes(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	if max <= 3 {
		return string(r[:max])
	}
	return string(r[:max-3]) + "..."
}

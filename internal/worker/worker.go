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

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
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

	// HeartbeatInterval defaults to 30s; tests shorten it.
	HeartbeatInterval time.Duration
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
	log.Info("worker started", "poll_interval", poll)
	defer log.Info("worker stopped")

	for claimCtx.Err() == nil {
		var wake <-chan struct{}
		if w.Waker != nil {
			wake = w.Waker.C()
		}
		job, err := db.ClaimJob(claimCtx, w.Pool, w.ID)
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
	workDir := w.Cfg.Downly.Worker.WorkDir
	defer func() { _ = os.RemoveAll(jobDir(workDir, job.ID)) }()

	runCtx, cancelRun := context.WithCancelCause(workCtx)
	defer cancelRun(nil)
	w.Controller.Register(job.ID, func() { cancelRun(errCanceledByUser) })
	defer w.Controller.Unregister(job.ID)
	go w.heartbeat(runCtx, log, job.ID, cancelRun)

	msgID := int(job.TelegramMsgID)
	progress := newProgressReporter(runCtx,
		func(ctx context.Context, text string, percent int) error {
			return db.UpdateProgress(ctx, w.Pool, job.ID, text, percent)
		},
		func(ctx context.Context, text string) error { return w.Msg.Edit(ctx, job.ChatID, msgID, text) },
		func(text string, percent int) string {
			return formatProgressMessage(job.ID, job.Status, text, percent, 0, 1, job.Priority)
		},
	)
	progress.Force(job.ProgressText, job.ProgressPercent)

	dlCtx, cancelDL := context.WithTimeout(runCtx, w.jobTimeout())
	res, err := w.download(dlCtx, log, job, progress)
	timedOut := errors.Is(dlCtx.Err(), context.DeadlineExceeded) && runCtx.Err() == nil
	cancelDL()

	if err == nil || runCtx.Err() != nil {
		if w.handleInterrupted(workCtx, runCtx, log, job) {
			return
		}
	}
	if err != nil {
		if timedOut {
			err = fmt.Errorf("timed out after %s", w.jobTimeout())
		}
		w.fail(workCtx, log, job, err, isPermanent(err))
		return
	}

	fi, err := os.Stat(res.FilePath)
	if err != nil {
		w.fail(workCtx, log, job, err, false)
		return
	}
	log.Info("download completed", "platform", res.Platform, "file_name", res.FileName, "size_bytes", fi.Size(), "media_type", res.Media)

	if limit := w.Cfg.Downly.Worker.MaxFileSizeMB; fi.Size() > limit*1024*1024 {
		msg := fmt.Sprintf("File too large: %.1fMB exceeds %dMB. Try a lower quality (e.g. send q480:<url>).", float64(fi.Size())/1024/1024, limit)
		w.fail(workCtx, log, job, errors.New(msg), true)
		return
	}

	progress.Force("Uploading to Telegram", 99)
	err = uploadWithRetry(runCtx, w.Msg, job.ChatID, res, fi.Size())
	if runCtx.Err() != nil && w.handleInterrupted(workCtx, runCtx, log, job) {
		return
	}
	if err != nil {
		if errors.Is(err, errForbidden) {
			fctx, cancel := w.finalizeCtx(workCtx)
			_ = db.MarkUserBlocked(fctx, w.Pool, job.ChatID)
			cancel()
		}
		w.fail(workCtx, log, job, fmt.Errorf("upload failed: %w", err), isPermanentUploadErr(err))
		return
	}

	fctx, cancel := w.finalizeCtx(workCtx)
	defer cancel()
	if err := db.MarkDone(fctx, w.Pool, job.ID, res.FilePath, res.FileName, res.Platform, fi.Size()); err != nil {
		log.Error("mark done failed", "error", err)
	}
	w.editFinal(fctx, job, formatDoneMessage(job.ID, res))
	log.Info("job finished", "platform", res.Platform, "file_name", res.FileName)
}

func (w *Worker) download(ctx context.Context, log *slog.Logger, job *db.Job, progress *progressReporter) (*downloader.Result, error) {
	workDir := w.Cfg.Downly.Worker.WorkDir
	switch {
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
				progress.Force(fmt.Sprintf("Quality %s unavailable, trying %s...", chain[i-1], q), 5)
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
func (w *Worker) handleInterrupted(workCtx, runCtx context.Context, log *slog.Logger, job *db.Job) bool {
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
		w.editFinal(fctx, job, fmt.Sprintf("Job #%d\nThe bot is restarting. Your download will resume automatically.", job.ID))
	case errors.Is(cause, errCanceledByUser):
		log.Info("job canceled by user")
		// /cancel already marked the row; this is a no-op unless it raced.
		_ = db.MarkCanceled(fctx, w.Pool, job.ID, "Canceled by user")
		w.editFinal(fctx, job, formatCanceledMessage(job.ID, "Canceled by user"))
	case errors.Is(cause, errJobLost):
		st, _ := db.GetJobStatus(fctx, w.Pool, job.ID)
		log.Warn("job taken away from worker", "status", st)
		if st == db.StatusCanceled {
			w.editFinal(fctx, job, formatCanceledMessage(job.ID, "Canceled by user"))
		}
	default:
		log.Warn("job context ended", "cause", cause)
	}
	return true
}

// fail records a failed attempt, scheduling a retry when it may help.
func (w *Worker) fail(workCtx context.Context, log *slog.Logger, job *db.Job, err error, permanent bool) {
	fctx, cancel := w.finalizeCtx(workCtx)
	defer cancel()
	userMsg := friendlyError(err)
	if !permanent && job.RetryCount < w.Cfg.Downly.Limits.MaxRetries {
		delay := retryDelay(job.RetryCount)
		log.Warn("download failed, scheduling retry", "error", err, "retry_count", job.RetryCount+1, "delay", delay)
		if dbErr := db.MarkFailedForRetry(fctx, w.Pool, job.ID, truncate(err.Error()), delay); dbErr != nil {
			log.Error("schedule retry failed", "error", dbErr)
			return
		}
		w.editFinal(fctx, job, fmt.Sprintf("Job #%d failed: %s\nRetrying automatically in %s...", job.ID, userMsg, delay.Round(time.Second)))
		return
	}
	log.Error("download failed", "error", err, "permanent", permanent, "retry_count", job.RetryCount)
	if dbErr := db.MarkFailed(fctx, w.Pool, job.ID, truncate(err.Error())); dbErr != nil {
		log.Error("mark failed failed", "error", dbErr)
	}
	w.editFinal(fctx, job, formatFailureMessage(job.ID, userMsg))
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

func formatProgressMessage(jobID int64, status db.JobStatus, progress string, percent, ahead, active, priority int) string {
	msg := fmt.Sprintf("Job #%d\nStatus: %s", jobID, status)
	if priority > 0 {
		msg += fmt.Sprintf("\nPriority: %d", priority)
	}
	if ahead > 0 {
		msg += fmt.Sprintf("\nQueue position: %d", ahead+1)
	}
	if active > 0 {
		msg += fmt.Sprintf("\nActive workers: %d", active)
	}
	if progress != "" {
		bar := progressBar(percent)
		msg += fmt.Sprintf("\n%s %d%%\n%s", bar, percent, progress)
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

func formatFailureMessage(jobID int64, err string) string {
	return fmt.Sprintf("Job #%d\nStatus: failed\nError: %s", jobID, err)
}

func formatCanceledMessage(jobID int64, reason string) string {
	return fmt.Sprintf("Job #%d\nStatus: canceled\nReason: %s", jobID, reason)
}

func formatDoneMessage(jobID int64, res *downloader.Result) string {
	msg := fmt.Sprintf("Job #%d\nStatus: done", jobID)
	if res.Platform != "" && res.Platform != "unknown" {
		msg += "\nSource: " + res.Platform
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

package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/dbtest"
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/media"
)

// fakeDL writes a small file, optionally blocking or failing first.
type fakeDL struct {
	mu    sync.Mutex
	calls int
	// fn decides the outcome of each call; nil means succeed.
	fn func(ctx context.Context, call int) error
}

func (f *fakeDL) run(ctx context.Context, workDir string, jobID int64) (*downloader.Result, error) {
	f.mu.Lock()
	f.calls++
	call := f.calls
	f.mu.Unlock()
	if f.fn != nil {
		if err := f.fn(ctx, call); err != nil {
			return nil, err
		}
	}
	dir := filepath.Join(workDir, fmt.Sprintf("job-%d", jobID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dir, "v.mp4")
	if err := os.WriteFile(p, []byte("video"), 0o644); err != nil {
		return nil, err
	}
	return &downloader.Result{FilePath: p, FileName: "v.mp4", Platform: "youtube", Media: downloader.MediaVideo, Title: "Test"}, nil
}

func (f *fakeDL) Download(ctx context.Context, workDir string, jobID int64, url string, _ func(string, int)) (*downloader.Result, error) {
	return f.run(ctx, workDir, jobID)
}
func (f *fakeDL) DownloadWithQuality(ctx context.Context, workDir string, jobID int64, url, q string, _ func(string, int)) (*downloader.Result, error) {
	return f.run(ctx, workDir, jobID)
}
func (f *fakeDL) DownloadAudio(ctx context.Context, workDir string, jobID int64, url string, _ func(string, int)) (*downloader.Result, error) {
	return f.run(ctx, workDir, jobID)
}

// blockUntilCanceled simulates a long download.
func blockUntilCanceled(ctx context.Context, _ int) error {
	<-ctx.Done()
	return ctx.Err()
}

type fakeMsg struct {
	mu        sync.Mutex
	edits     []string
	uploads   int
	captions  []string
	inline    []string
	uploadErr func(attempt int) error
}

func (m *fakeMsg) Edit(_ context.Context, _ int64, _ int, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.edits = append(m.edits, text)
	return nil
}
func (m *fakeMsg) Send(context.Context, int64, string) error { return nil }
func (m *fakeMsg) SendResult(ctx context.Context, _ int64, res *downloader.Result, opts SendOptions) ([]media.Item, error) {
	m.mu.Lock()
	m.captions = append(m.captions, opts.Caption)
	m.uploads++
	n := m.uploads
	m.mu.Unlock()
	if _, err := os.Stat(res.FilePath); err != nil {
		return nil, err
	}
	if m.uploadErr != nil {
		if err := m.uploadErr(n); err != nil {
			return nil, err
		}
	}
	return []media.Item{{Kind: media.Video, FileID: fmt.Sprintf("fid-%d", n)}}, nil
}
func (m *fakeMsg) SendCached(context.Context, int64, []media.Item, media.Meta, SendOptions) error {
	return nil
}
func (m *fakeMsg) EditInlineMedia(_ context.Context, id string, item media.Item, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inline = append(m.inline, "media:"+id+":"+item.FileID)
	return nil
}
func (m *fakeMsg) EditInlineText(_ context.Context, id, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inline = append(m.inline, "text:"+id+":"+text)
	return nil
}
func (m *fakeMsg) Delete(context.Context, int64, int) error { return nil }
func (m *fakeMsg) lastEdit() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.edits) == 0 {
		return ""
	}
	return m.edits[len(m.edits)-1]
}

type harness struct {
	t    *testing.T
	pool *pgxpool.Pool
	cfg  *config.Root
	dl   *fakeDL
	msg  *fakeMsg
	ctrl *Controller
	w    *Worker
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := dbtest.NewPool(t)
	cfg := &config.Root{}
	cfg.Downly.Worker.WorkDir = t.TempDir()
	cfg.Downly.Worker.PollIntervalSec = 1
	cfg.Downly.Worker.MaxFileSizeMB = 50
	cfg.Downly.Worker.JobTimeoutMinutes = 30
	cfg.Downly.Limits.MaxRetries = 2
	h := &harness{t: t, pool: pool, cfg: cfg, dl: &fakeDL{}, msg: &fakeMsg{}, ctrl: NewController()}
	h.w = &Worker{
		ID: "test-1", Cfg: cfg, Pool: pool, DL: h.dl, Msg: h.msg, Controller: h.ctrl,
		Log:               slog.New(slog.NewTextHandler(io.Discard, nil)),
		HeartbeatInterval: 50 * time.Millisecond,
	}
	uploadBackoff = 10 * time.Millisecond
	return h
}

func (h *harness) enqueue() int64 {
	h.t.Helper()
	id, err := db.InsertJob(context.Background(), h.pool, db.NewJob{ChatID: 7, UserID: 7, URL: "https://example.com/v", TelegramMsgID: 1})
	if err != nil {
		h.t.Fatal(err)
	}
	return id
}

// runOne claims and processes a single job synchronously.
func (h *harness) runOne(workCtx context.Context) *db.Job {
	h.t.Helper()
	job, err := db.ClaimJob(context.Background(), h.pool, h.w.ID)
	if err != nil || job == nil {
		h.t.Fatalf("claim: %v %v", job, err)
	}
	h.w.process(workCtx, h.w.Log, job)
	return job
}

type row struct {
	status     db.JobStatus
	retries    int
	errMsg     string
	nextInSecs float64
	startedNil bool
}

func (h *harness) row(id int64) row {
	h.t.Helper()
	var r row
	var started *time.Time
	err := h.pool.QueryRow(context.Background(), `
		select status, retry_count, error_message, extract(epoch from next_attempt_at - now()), started_at
		from download_jobs where id = $1`, id).Scan(&r.status, &r.retries, &r.errMsg, &r.nextInSecs, &started)
	if err != nil {
		h.t.Fatal(err)
	}
	r.startedNil = started == nil
	return r
}

func (h *harness) waitFor(cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	h.t.Fatal("condition not met in time")
}

func TestWorkerSuccess(t *testing.T) {
	h := newHarness(t)
	id := h.enqueue()
	h.runOne(context.Background())

	if r := h.row(id); r.status != db.StatusDone {
		t.Fatalf("status = %s", r.status)
	}
	if !strings.Contains(h.msg.lastEdit(), "Status: done") {
		t.Fatalf("final edit = %q", h.msg.lastEdit())
	}
	if _, err := os.Stat(jobDir(h.cfg.Downly.Worker.WorkDir, id)); !os.IsNotExist(err) {
		t.Fatal("job dir should be removed")
	}
}

func TestWorkerTransientFailureSchedulesBackoff(t *testing.T) {
	h := newHarness(t)
	h.dl.fn = func(context.Context, int) error { return errors.New("HTTP Error 503") }
	id := h.enqueue()
	h.runOne(context.Background())

	r := h.row(id)
	if r.status != db.StatusPending || r.retries != 1 {
		t.Fatalf("row = %+v", r)
	}
	if r.nextInSecs < 25 || r.nextInSecs > 31 {
		t.Fatalf("next attempt in %.0fs, want ~30s", r.nextInSecs)
	}
	if !strings.Contains(h.msg.lastEdit(), "Retrying automatically") {
		t.Fatalf("edit = %q", h.msg.lastEdit())
	}
	// Not due yet, so nothing is claimable.
	if job, _ := db.ClaimJob(context.Background(), h.pool, "x"); job != nil {
		t.Fatal("job claimed before its backoff elapsed")
	}
}

func TestWorkerPermanentFailureDoesNotRetry(t *testing.T) {
	h := newHarness(t)
	h.dl.fn = func(context.Context, int) error { return errors.New("ERROR: [youtube] x: Private video") }
	id := h.enqueue()
	h.runOne(context.Background())
	if r := h.row(id); r.status != db.StatusFailed {
		t.Fatalf("row = %+v", r)
	}
	if !strings.Contains(h.msg.lastEdit(), "Private video") {
		t.Fatalf("edit = %q", h.msg.lastEdit())
	}
}

func TestWorkerRetriesExhausted(t *testing.T) {
	h := newHarness(t)
	h.dl.fn = func(context.Context, int) error { return errors.New("HTTP Error 503") }
	id := h.enqueue()
	for i := 0; i <= h.cfg.Downly.Limits.MaxRetries; i++ {
		_, _ = h.pool.Exec(context.Background(), `update download_jobs set next_attempt_at = now() where id = $1`, id)
		h.runOne(context.Background())
	}
	if r := h.row(id); r.status != db.StatusFailed || r.retries != 2 {
		t.Fatalf("row = %+v", r)
	}
}

func TestWorkerUserCancel(t *testing.T) {
	h := newHarness(t)
	h.dl.fn = blockUntilCanceled
	id := h.enqueue()
	done := make(chan struct{})
	go func() { h.runOne(context.Background()); close(done) }()

	h.waitFor(func() bool { return h.ctrl.Cancel(id) })
	<-done
	if r := h.row(id); r.status != db.StatusCanceled {
		t.Fatalf("row = %+v", r)
	}
	if !strings.Contains(h.msg.lastEdit(), "canceled") {
		t.Fatalf("edit = %q", h.msg.lastEdit())
	}
}

// A /cancel handled by another instance only changes the DB row; the
// worker must notice via its heartbeat.
func TestWorkerStopsWhenCanceledElsewhere(t *testing.T) {
	h := newHarness(t)
	h.dl.fn = blockUntilCanceled
	id := h.enqueue()
	done := make(chan struct{})
	go func() { h.runOne(context.Background()); close(done) }()

	h.waitFor(func() bool { return h.row(id).status == db.StatusProcessing })
	prev, err := db.CancelJob(context.Background(), h.pool, id, 7)
	if err != nil || prev != db.StatusProcessing {
		t.Fatalf("CancelJob = %v %v", prev, err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop after remote cancel")
	}
	if r := h.row(id); r.status != db.StatusCanceled {
		t.Fatalf("row = %+v", r)
	}
	if h.msg.uploads != 0 {
		t.Fatal("canceled job must not be uploaded")
	}
}

func TestWorkerShutdownRequeuesWithoutCountingRetry(t *testing.T) {
	h := newHarness(t)
	h.dl.fn = blockUntilCanceled
	id := h.enqueue()
	workCtx, stopWork := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.runOne(workCtx); close(done) }()

	h.waitFor(func() bool { return h.row(id).status == db.StatusProcessing })
	stopWork()
	<-done

	r := h.row(id)
	if r.status != db.StatusPending || r.retries != 0 || !r.startedNil {
		t.Fatalf("row = %+v", r)
	}
	if !strings.Contains(h.msg.lastEdit(), "restarting") {
		t.Fatalf("edit = %q", h.msg.lastEdit())
	}
	// It is immediately claimable by the next process.
	if job, _ := db.ClaimJob(context.Background(), h.pool, "next"); job == nil || job.ID != id {
		t.Fatal("requeued job should be claimable right away")
	}
}

func TestWorkerJobTimeoutIsRetryable(t *testing.T) {
	h := newHarness(t)
	h.dl.fn = blockUntilCanceled
	id := h.enqueue()
	// JobTimeoutMinutes=0 yields an immediately expired deadline.
	h.cfg.Downly.Worker.JobTimeoutMinutes = 0
	ctx := context.Background()
	job, _ := db.ClaimJob(ctx, h.pool, h.w.ID)
	h.w.process(ctx, h.w.Log, job)
	r := h.row(id)
	if r.status != db.StatusPending || !strings.Contains(r.errMsg, "timed out") {
		t.Fatalf("row = %+v", r)
	}
}

func TestWorkerUploadRetriesTransientErrors(t *testing.T) {
	h := newHarness(t)
	h.msg.uploadErr = func(n int) error {
		if n < 3 {
			return errors.New("connection reset by peer")
		}
		return nil
	}
	id := h.enqueue()
	h.runOne(context.Background())
	if r := h.row(id); r.status != db.StatusDone {
		t.Fatalf("row = %+v", r)
	}
	if h.msg.uploads != 3 {
		t.Fatalf("uploads = %d, want 3", h.msg.uploads)
	}
	if h.dl.calls != 1 {
		t.Fatalf("upload retries must not re-download, downloads = %d", h.dl.calls)
	}
}

func TestWorkerUploadForbiddenMarksUserBlocked(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_ = db.TouchUser(ctx, h.pool, 7, 7, "", "")
	h.msg.uploadErr = func(int) error { return fmt.Errorf("%w, bot was blocked by the user", bot.ErrorForbidden) }
	id := h.enqueue()
	h.runOne(ctx)
	if r := h.row(id); r.status != db.StatusFailed {
		t.Fatalf("row = %+v", r)
	}
	if h.msg.uploads != 1 {
		t.Fatalf("permanent upload errors must not be retried, uploads = %d", h.msg.uploads)
	}
	ids, _ := db.GetAllChatIDs(ctx, h.pool)
	if len(ids) != 0 {
		t.Fatalf("blocked user still a broadcast target: %v", ids)
	}
}

func TestReaperRecoversDeadWorker(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.enqueue()
	if _, err := db.ClaimJob(ctx, h.pool, "crashed"); err != nil {
		t.Fatal(err)
	}
	_, _ = h.pool.Exec(ctx, `update download_jobs set heartbeat_at = now() - interval '10 minutes' where id = $1`, id)

	requeued, failed, err := db.ReapStuckJobs(ctx, h.pool, 5*time.Minute, 2)
	if err != nil || requeued != 1 || failed != 0 {
		t.Fatalf("reap = %d %d %v", requeued, failed, err)
	}
	if r := h.row(id); r.status != db.StatusPending || r.retries != 1 {
		t.Fatalf("row = %+v", r)
	}

	// A live job (fresh heartbeat) is left alone.
	if _, err := db.ClaimJob(ctx, h.pool, "alive"); err != nil {
		t.Fatal(err)
	}
	requeued, failed, _ = db.ReapStuckJobs(ctx, h.pool, 5*time.Minute, 2)
	if requeued != 0 || failed != 0 {
		t.Fatalf("live job reaped: %d %d", requeued, failed)
	}

	// Out of retries: failed instead of requeued.
	_, _ = h.pool.Exec(ctx, `update download_jobs set heartbeat_at = now() - interval '10 minutes', retry_count = 2 where id = $1`, id)
	_, failed, _ = db.ReapStuckJobs(ctx, h.pool, 5*time.Minute, 2)
	if failed != 1 || h.row(id).status != db.StatusFailed {
		t.Fatalf("expected failure when out of retries, row=%+v", h.row(id))
	}
}

func TestLateWritesAfterCancelAreIgnored(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := h.enqueue()
	if _, err := db.ClaimJob(ctx, h.pool, "w"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CancelJob(ctx, h.pool, id, 7); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkDone(ctx, h.pool, id, "", "", "", 0); !errors.Is(err, db.ErrNotProcessing) {
		t.Fatalf("MarkDone on canceled job: %v", err)
	}
	if h.row(id).status != db.StatusCanceled {
		t.Fatal("cancel was overwritten")
	}
}

// With LISTEN/NOTIFY a new job is picked up immediately even with a very
// long poll interval.
func TestWorkerWakesOnNotify(t *testing.T) {
	h := newHarness(t)
	h.cfg.Downly.Worker.PollIntervalSec = 3600
	waker := NewWaker()
	h.w.Waker = waker
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Listen(ctx, h.w.Log, h.pool, waker)
	go h.w.Run(ctx, ctx)

	time.Sleep(300 * time.Millisecond) // let the worker go idle
	id := h.enqueue()
	h.waitFor(func() bool { return h.row(id).status == db.StatusDone })
}

func TestSweepWorkDir(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "job-1")
	fresh := filepath.Join(dir, "job-2")
	other := filepath.Join(dir, "keep")
	for _, d := range []string{old, fresh, other} {
		_ = os.MkdirAll(d, 0o755)
	}
	past := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(old, past, past)
	_ = os.Chtimes(other, past, past)
	SweepWorkDir(slog.New(slog.NewTextHandler(io.Discard, nil)), dir, time.Hour)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("stale job dir should be removed")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh job dir should be kept")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("non-job dirs must be kept")
	}
}

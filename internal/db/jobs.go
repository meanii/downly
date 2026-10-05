package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type JobStatus string

const (
	StatusPending    JobStatus = "pending"
	StatusProcessing JobStatus = "processing"
	StatusDone       JobStatus = "done"
	StatusFailed     JobStatus = "failed"
	StatusCanceled   JobStatus = "canceled"
)

// JobMode selects what the worker extracts.
type JobMode string

const (
	ModeVideo JobMode = "video"
	ModeAudio JobMode = "audio"
	// ModeGIF converts (a section of) a video into a Telegram GIF.
	ModeGIF JobMode = "gif"
)

type Job struct {
	ID              int64
	ChatID          int64
	UserID          int64
	URL             string
	Mode            JobMode
	Quality         string // "" means best available
	Platform        string
	Status          JobStatus
	Priority        int
	OutputPath      string
	OutputName      string
	ErrorMessage    string
	RetryCount      int
	TelegramMsgID   int64
	ProgressText    string
	ProgressPercent int
	QueuePosition   int
	CacheKey        string
	Cached          bool
	InlineMessageID string
	ReplyTo         int64
	// ClipStart/ClipEnd select a section in seconds (both 0 = whole video).
	ClipStart  int
	ClipEnd    int
	CreatedAt  time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

// NewJob holds what is needed to enqueue a download.
type NewJob struct {
	ChatID        int64
	UserID        int64
	URL           string
	Mode          JobMode
	Quality       string
	TelegramMsgID int64
	Priority      int
	// CacheKey identifies the content for the media cache ("" = uncacheable).
	CacheKey string
	// InlineMessageID is set for inline-mode requests.
	InlineMessageID string
	// ReplyTo is the message the job answers (0 = none).
	ReplyTo int64
	// ClipStart/ClipEnd select a section in seconds (both 0 = whole video).
	ClipStart, ClipEnd int
}

type QueueStats struct {
	PendingAhead int
	Active       int
	UserPending  int
}

// jobColumns and scanJob keep every full-row query in sync.
const jobColumns = `id, chat_id, user_id, url, mode, quality, platform, status, priority, output_path, output_name,
	error_message, retry_count, telegram_message_id, progress_text, progress_percent, cache_key, cached,
	inline_message_id, reply_to_message_id, clip_start, clip_end, created_at, started_at, finished_at`

func scanJob(row pgx.Row) (Job, error) {
	var job Job
	err := row.Scan(&job.ID, &job.ChatID, &job.UserID, &job.URL, &job.Mode, &job.Quality, &job.Platform, &job.Status,
		&job.Priority, &job.OutputPath, &job.OutputName, &job.ErrorMessage, &job.RetryCount, &job.TelegramMsgID,
		&job.ProgressText, &job.ProgressPercent, &job.CacheKey, &job.Cached, &job.InlineMessageID,
		&job.ReplyTo, &job.ClipStart, &job.ClipEnd, &job.CreatedAt, &job.StartedAt, &job.FinishedAt)
	return job, err
}

func queryJobs(ctx context.Context, pool *pgxpool.Pool, sql string, args ...any) ([]Job, error) {
	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func InsertJob(ctx context.Context, pool *pgxpool.Pool, j NewJob) (int64, error) {
	if j.Mode == "" {
		j.Mode = ModeVideo
	}
	var jobID int64
	err := pool.QueryRow(ctx, `
		insert into download_jobs (chat_id, user_id, url, mode, quality, status, priority, telegram_message_id,
			progress_text, progress_percent, cache_key, inline_message_id, reply_to_message_id, clip_start, clip_end)
		values ($1, $2, $3, $4, $5, $6, $7, $8, 'Queued', 0, $9, $10, $11, $12, $13)
		returning id
	`, j.ChatID, j.UserID, j.URL, j.Mode, j.Quality, StatusPending, j.Priority, j.TelegramMsgID, j.CacheKey, j.InlineMessageID, j.ReplyTo, j.ClipStart, j.ClipEnd).Scan(&jobID)
	return jobID, err
}

// ErrNotProcessing is returned when a state change targets a job that is no
// longer processing, e.g. because it was canceled or reaped meanwhile.
var ErrNotProcessing = errors.New("job is no longer processing")

func expectProcessing(cmd pgconn.CommandTag, err error) error {
	if err != nil {
		return err
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotProcessing
	}
	return nil
}

// ClaimJob atomically takes the next due pending job.
func ClaimJob(ctx context.Context, pool *pgxpool.Pool, workerID string) (*Job, error) {
	return ClaimJobLimited(ctx, pool, workerID, 0)
}

// ClaimJobLimited is ClaimJob, skipping users who already have maxPerUser
// jobs processing (0 = no cap). Two workers claiming at the same instant can
// overshoot the cap by one; that is accepted to keep claims lock-free.
func ClaimJobLimited(ctx context.Context, pool *pgxpool.Pool, workerID string, maxPerUser int) (*Job, error) {
	row := pool.QueryRow(ctx, `
		update download_jobs
		set status = $2, started_at = now(), heartbeat_at = now(), worker_id = $3,
			progress_text = 'Starting download', progress_percent = 1
		where id = (
			select d.id from download_jobs d
			where d.status = $1 and d.next_attempt_at <= now()
			and ($4 <= 0 or (
				select count(*) from download_jobs p where p.user_id = d.user_id and p.status = $2
			) < $4)
			order by d.priority desc, d.created_at asc
			for update skip locked
			limit 1
		)
		returning `+jobColumns, StatusPending, StatusProcessing, workerID, maxPerUser)
	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// Heartbeat marks a processing job as alive. It reports false if the job is
// no longer processing (canceled elsewhere or reaped), so the worker can stop.
func Heartbeat(ctx context.Context, pool *pgxpool.Pool, jobID int64) (bool, error) {
	cmd, err := pool.Exec(ctx, `update download_jobs set heartbeat_at = now() where id = $1 and status = $2`, jobID, StatusProcessing)
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() > 0, nil
}

func MarkDone(ctx context.Context, pool *pgxpool.Pool, jobID int64, outputPath, outputName, platform string, fileSizeBytes int64) error {
	return expectProcessing(pool.Exec(ctx, `
		update download_jobs
		set status = $2, output_path = $3, output_name = $4, platform = $5, error_message = '', progress_text = 'Completed',
			progress_percent = 100, file_size_bytes = $6, finished_at = now()
		where id = $1 and status = $7
	`, jobID, StatusDone, outputPath, outputName, platform, fileSizeBytes, StatusProcessing))
}

// MarkFailedForRetry puts a processing job back in the queue, due after delay.
func MarkFailedForRetry(ctx context.Context, pool *pgxpool.Pool, jobID int64, errMsg string, delay time.Duration) error {
	return expectProcessing(pool.Exec(ctx, `
		update download_jobs
		set status = $2, error_message = $3, retry_count = retry_count + 1, progress_text = 'Queued (retry)',
			started_at = null, finished_at = null, heartbeat_at = null,
			next_attempt_at = now() + make_interval(secs => $4)
		where id = $1 and status = $5
	`, jobID, StatusPending, errMsg, delay.Seconds(), StatusProcessing))
}

// RequeueJob returns a processing job to the queue without counting a retry,
// e.g. when the worker is shutting down.
func RequeueJob(ctx context.Context, pool *pgxpool.Pool, jobID int64, reason string) error {
	return expectProcessing(pool.Exec(ctx, `
		update download_jobs
		set status = $2, progress_text = $3, progress_percent = 0, started_at = null, heartbeat_at = null,
			next_attempt_at = now()
		where id = $1 and status = $4
	`, jobID, StatusPending, reason, StatusProcessing))
}

func MarkFailed(ctx context.Context, pool *pgxpool.Pool, jobID int64, errMsg string) error {
	return expectProcessing(pool.Exec(ctx, `
		update download_jobs
		set status = $2, error_message = $3, progress_text = 'Failed', finished_at = now()
		where id = $1 and status = $4
	`, jobID, StatusFailed, errMsg, StatusProcessing))
}

func MarkCanceled(ctx context.Context, pool *pgxpool.Pool, jobID int64, reason string) error {
	return expectProcessing(pool.Exec(ctx, `
		update download_jobs
		set status = $2, progress_text = 'Canceled', error_message = $3, finished_at = now()
		where id = $1 and status = $4
	`, jobID, StatusCanceled, reason, StatusProcessing))
}

// CancelJob cancels a user's pending or processing job. It returns the
// status the job had (pending or processing), or "" if nothing was canceled.
// A processing job's worker notices on its next heartbeat, even if it runs
// in another instance.
func CancelJob(ctx context.Context, pool *pgxpool.Pool, jobID, userID int64) (JobStatus, error) {
	var prev JobStatus
	err := pool.QueryRow(ctx, `
		update download_jobs d
		set status = $3, progress_text = 'Canceled', error_message = 'Canceled by user', finished_at = now()
		from (select id, status from download_jobs where id = $1 for update) old
		where d.id = old.id and d.user_id = $2 and d.status in ($4, $5)
		returning old.status
	`, jobID, userID, StatusCanceled, StatusPending, StatusProcessing).Scan(&prev)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return prev, err
}

// GetJobStatus returns a job's current status.
func GetJobStatus(ctx context.Context, pool *pgxpool.Pool, jobID int64) (JobStatus, error) {
	var st JobStatus
	err := pool.QueryRow(ctx, `select status from download_jobs where id = $1`, jobID).Scan(&st)
	return st, err
}

func OwnsJob(ctx context.Context, pool *pgxpool.Pool, jobID, userID int64) (bool, JobStatus, error) {
	var status JobStatus
	var foundUserID int64
	if err := pool.QueryRow(ctx, `select user_id, status from download_jobs where id = $1`, jobID).Scan(&foundUserID, &status); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, "", nil
		}
		return false, "", err
	}
	return foundUserID == userID, status, nil
}

func UpdatePriority(ctx context.Context, pool *pgxpool.Pool, jobID int64, priority int) (bool, error) {
	cmd, err := pool.Exec(ctx, `
		update download_jobs set priority = $2 where id = $1 and status = $3
	`, jobID, priority, StatusPending)
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() > 0, nil
}

func UserActiveCounts(ctx context.Context, pool *pgxpool.Pool, userID int64) (queued int, processing int, err error) {
	err = pool.QueryRow(ctx, `
		select count(*) filter (where status = $2), count(*) filter (where status = $3)
		from download_jobs where user_id = $1 and status in ($2, $3)
	`, userID, StatusPending, StatusProcessing).Scan(&queued, &processing)
	return
}

func UpdateProgress(ctx context.Context, pool *pgxpool.Pool, jobID int64, progressText string, progressPercent int) error {
	_, err := pool.Exec(ctx, `
		update download_jobs
		set progress_text = $2, progress_percent = $3
		where id = $1
	`, jobID, progressText, progressPercent)
	return err
}

// pendingAheadSQL counts pending jobs that will be claimed before job $2.
const pendingAheadSQL = `
	select count(*)
	from download_jobs d, (select priority, created_at from download_jobs where id = $2) me
	where d.status = $1 and (
		d.priority > me.priority or (d.priority = me.priority and d.created_at < me.created_at)
	)`

func GetQueueStats(ctx context.Context, pool *pgxpool.Pool, jobID, userID int64) (*QueueStats, error) {
	stats := &QueueStats{}
	if err := pool.QueryRow(ctx, pendingAheadSQL, StatusPending, jobID).Scan(&stats.PendingAhead); err != nil {
		return nil, err
	}
	if err := pool.QueryRow(ctx, `
		select count(*) filter (where status = $1), count(*) filter (where user_id = $2)
		from download_jobs where status in ($1, $3)
	`, StatusProcessing, userID, StatusPending).Scan(&stats.Active, &stats.UserPending); err != nil {
		return nil, err
	}
	return stats, nil
}

func GetUserJobs(ctx context.Context, pool *pgxpool.Pool, userID int64, limit int) ([]Job, error) {
	jobs, err := queryJobs(ctx, pool, `
		select `+jobColumns+`
		from download_jobs
		where user_id = $1
		order by created_at desc
		limit $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	for i := range jobs {
		if jobs[i].Status == StatusPending {
			var ahead int
			if err := pool.QueryRow(ctx, pendingAheadSQL, StatusPending, jobs[i].ID).Scan(&ahead); err == nil {
				jobs[i].QueuePosition = ahead + 1
			}
		}
	}
	return jobs, nil
}

func GetUserHistory(ctx context.Context, pool *pgxpool.Pool, userID int64, limit int) ([]Job, error) {
	return queryJobs(ctx, pool, `
		select `+jobColumns+`
		from download_jobs
		where user_id = $1 and status in ('done', 'failed', 'canceled')
		order by created_at desc
		limit $2
	`, userID, limit)
}

func GetActiveJobs(ctx context.Context, pool *pgxpool.Pool, limit int) ([]Job, error) {
	return queryJobs(ctx, pool, `
		select `+jobColumns+`
		from download_jobs
		where status in ('pending', 'processing')
		order by status desc, priority desc, created_at asc
		limit $1
	`, limit)
}

func UserDailyJobCount(ctx context.Context, pool *pgxpool.Pool, userID int64) (int, error) {
	var count int
	err := pool.QueryRow(ctx, `
		select count(*) from download_jobs
		where user_id = $1 and not cached and created_at >= now() - interval '24 hours'
	`, userID).Scan(&count)
	return count, err
}

// ReapStuckJobs recovers processing jobs whose worker stopped sending
// heartbeats (crash, OOM kill, lost connection). Jobs with retries left go
// back to the queue; the rest are failed.
func ReapStuckJobs(ctx context.Context, pool *pgxpool.Pool, staleAfter time.Duration, maxRetries int) (requeued, failed int64, err error) {
	rows, err := pool.Query(ctx, `
		update download_jobs d
		set status = case when d.retry_count < $3 then 'pending' else 'failed' end,
			retry_count = d.retry_count + 1,
			started_at = case when d.retry_count < $3 then null else d.started_at end,
			finished_at = case when d.retry_count < $3 then null else now() end,
			heartbeat_at = null,
			next_attempt_at = now(),
			progress_text = case when d.retry_count < $3 then 'Queued (recovered)' else 'Failed' end,
			error_message = 'Worker stopped responding'
		from (
			select id from download_jobs
			where status = $1 and coalesce(heartbeat_at, started_at, created_at) < now() - make_interval(secs => $2)
			for update skip locked
		) stale
		where d.id = stale.id
		returning d.status
	`, StatusProcessing, staleAfter.Seconds(), maxRetries)
	if err != nil {
		return 0, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		var st JobStatus
		if err := rows.Scan(&st); err != nil {
			return requeued, failed, err
		}
		if st == StatusPending {
			requeued++
		} else {
			failed++
		}
	}
	return requeued, failed, rows.Err()
}

// PruneJobs deletes finished jobs older than the retention window and folds
// them into job_stats_daily in the same statement, so totals survive.
func PruneJobs(ctx context.Context, pool *pgxpool.Pool, retentionHours int) (int64, error) {
	var n int64
	err := pool.QueryRow(ctx, `
		with deleted as (
			delete from download_jobs
			where status in ('done', 'failed', 'canceled')
			and finished_at is not null
			and finished_at < now() - make_interval(hours => $1)
			returning user_id, platform, status, file_size_bytes, finished_at
		), rolled as (
			insert into job_stats_daily (day, user_id, platform, status, jobs, bytes, last_at)
			select (finished_at at time zone 'UTC')::date, user_id, platform, status, count(*), coalesce(sum(file_size_bytes), 0), max(finished_at)
			from deleted
			group by 1, 2, 3, 4
			on conflict (day, user_id, platform, status) do update
			set jobs = job_stats_daily.jobs + excluded.jobs,
			    bytes = job_stats_daily.bytes + excluded.bytes,
			    last_at = greatest(job_stats_daily.last_at, excluded.last_at)
		)
		select count(*) from deleted
	`, retentionHours).Scan(&n)
	return n, err
}

// ModeLabel is a short tag for job listings, e.g. "audio" or "720p".
func ModeLabel(job Job) string {
	if job.Mode == ModeAudio {
		return "audio"
	}
	if job.Mode == ModeGIF {
		return "gif"
	}
	if job.ClipEnd > job.ClipStart {
		return fmt.Sprintf("clip %d-%ds", job.ClipStart, job.ClipEnd)
	}
	switch job.Quality {
	case "q360":
		return "360p"
	case "q480":
		return "480p"
	case "q720":
		return "720p"
	case "q1080":
		return "1080p"
	case "telegram":
		return "telegram"
	}
	return ""
}

func FormatActiveJobs(jobs []Job) string {
	if len(jobs) == 0 {
		return "No active or pending jobs."
	}
	lines := []string{fmt.Sprintf("Active/pending jobs (%d):", len(jobs))}
	for _, job := range jobs {
		line := fmt.Sprintf("#%d | %s | user %d | %s", job.ID, job.Status, job.UserID, trimURL(job.URL))
		if l := ModeLabel(job); l != "" {
			line += " | " + l
		}
		if job.ProgressText != "" {
			line += fmt.Sprintf(" | %s", job.ProgressText)
		}
		if job.ProgressPercent > 0 {
			line += fmt.Sprintf(" (%d%%)", job.ProgressPercent)
		}
		lines = append(lines, line)
	}
	return joinLines(lines)
}

func trimURL(s string) string {
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

func joinLines(lines []string) string {
	out := ""
	for i, line := range lines {
		if i > 0 {
			out += "\n"
		}
		out += line
	}
	return out
}

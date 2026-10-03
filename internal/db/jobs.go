package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
	CreatedAt       time.Time
	StartedAt       *time.Time
	FinishedAt      *time.Time
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
}

type QueueStats struct {
	PendingAhead int
	Active       int
	UserPending  int
}

// jobColumns and scanJob keep every full-row query in sync.
const jobColumns = `id, chat_id, user_id, url, mode, quality, platform, status, priority, output_path, output_name,
	error_message, retry_count, telegram_message_id, progress_text, progress_percent, created_at, started_at, finished_at`

func scanJob(row pgx.Row) (Job, error) {
	var job Job
	err := row.Scan(&job.ID, &job.ChatID, &job.UserID, &job.URL, &job.Mode, &job.Quality, &job.Platform, &job.Status,
		&job.Priority, &job.OutputPath, &job.OutputName, &job.ErrorMessage, &job.RetryCount, &job.TelegramMsgID,
		&job.ProgressText, &job.ProgressPercent, &job.CreatedAt, &job.StartedAt, &job.FinishedAt)
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
		insert into download_jobs (chat_id, user_id, url, mode, quality, status, priority, telegram_message_id, progress_text, progress_percent)
		values ($1, $2, $3, $4, $5, $6, $7, $8, 'Queued', 0)
		returning id
	`, j.ChatID, j.UserID, j.URL, j.Mode, j.Quality, StatusPending, j.Priority, j.TelegramMsgID).Scan(&jobID)
	return jobID, err
}

func ClaimJob(ctx context.Context, pool *pgxpool.Pool) (*Job, error) {
	row := pool.QueryRow(ctx, `
		update download_jobs
		set status = $2, started_at = now(), progress_text = 'Starting download', progress_percent = 1
		where id = (
			select id from download_jobs
			where status = $1
			order by priority desc, created_at asc
			for update skip locked
			limit 1
		)
		returning `+jobColumns, StatusPending, StatusProcessing)
	job, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func MarkDone(ctx context.Context, pool *pgxpool.Pool, jobID int64, outputPath, outputName, platform string, fileSizeBytes int64) error {
	_, err := pool.Exec(ctx, `
		update download_jobs
		set status = $2, output_path = $3, output_name = $4, platform = $5, error_message = '', progress_text = $6, progress_percent = $7, file_size_bytes = $8, finished_at = now()
		where id = $1
	`, jobID, StatusDone, outputPath, outputName, platform, "Completed", 100, fileSizeBytes)
	return err
}

func MarkFailedForRetry(ctx context.Context, pool *pgxpool.Pool, jobID int64, errMsg string) error {
	_, err := pool.Exec(ctx, `
		update download_jobs
		set status = $2, error_message = $3, retry_count = retry_count + 1, progress_text = $4, started_at = null, finished_at = null
		where id = $1
	`, jobID, StatusPending, errMsg, "Queued (retry)")
	return err
}

func MarkFailed(ctx context.Context, pool *pgxpool.Pool, jobID int64, errMsg string) error {
	_, err := pool.Exec(ctx, `
		update download_jobs
		set status = $2, error_message = $3, retry_count = retry_count + 1, progress_text = $4, finished_at = now()
		where id = $1
	`, jobID, StatusFailed, errMsg, "Failed")
	return err
}

func MarkCanceled(ctx context.Context, pool *pgxpool.Pool, jobID int64, reason string) error {
	_, err := pool.Exec(ctx, `
		update download_jobs
		set status = $2, progress_text = $3, error_message = $4, finished_at = now()
		where id = $1
	`, jobID, StatusCanceled, "Canceled", reason)
	return err
}

func CancelPendingJob(ctx context.Context, pool *pgxpool.Pool, jobID, userID int64) (bool, error) {
	cmd, err := pool.Exec(ctx, `
		update download_jobs
		set status = $3, progress_text = $4, error_message = $5, finished_at = now()
		where id = $1 and user_id = $2 and status = $6
	`, jobID, userID, StatusCanceled, "Canceled", "Canceled by user", StatusPending)
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() > 0, nil
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
		where user_id = $1 and created_at >= now() - interval '24 hours'
	`, userID).Scan(&count)
	return count, err
}

func ReapStuckJobs(ctx context.Context, pool *pgxpool.Pool, stuckMinutes int) (int64, error) {
	cmd, err := pool.Exec(ctx, `
		update download_jobs
		set status = $1, error_message = 'Worker timeout: job stuck in processing', progress_text = 'Reaped', finished_at = now()
		where status = $2 and started_at < now() - make_interval(mins := $3)
	`, StatusFailed, StatusProcessing, stuckMinutes)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
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

// modeLabel is a short tag for job listings, e.g. "audio" or "720p".
func modeLabel(job Job) string {
	if job.Mode == ModeAudio {
		return "audio"
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

func FormatUserQueueSummary(jobs []Job) string {
	if len(jobs) == 0 {
		return "You have no recent jobs. Send a URL to queue a download."
	}
	lines := []string{"Your recent jobs:"}
	for _, job := range jobs {
		line := fmt.Sprintf("#%d | %s | %s", job.ID, job.Status, trimURL(job.URL))
		if l := modeLabel(job); l != "" {
			line += " | " + l
		}
		if job.Priority > 0 {
			line += fmt.Sprintf(" | priority %d", job.Priority)
		}
		if job.Status == StatusPending && job.QueuePosition > 0 {
			line += fmt.Sprintf(" | position %d", job.QueuePosition)
		}
		if job.ProgressText != "" {
			line += fmt.Sprintf(" | %s", job.ProgressText)
		}
		if job.ProgressPercent > 0 {
			line += fmt.Sprintf(" (%d%%)", job.ProgressPercent)
		}
		if job.Status == StatusCanceled && job.ErrorMessage != "" {
			line += fmt.Sprintf(" | reason: %s", job.ErrorMessage)
		}
		lines = append(lines, line)
	}
	return joinLines(lines)
}

func FormatUserHistory(jobs []Job) string {
	if len(jobs) == 0 {
		return "No download history yet."
	}
	lines := []string{"Your download history:"}
	for _, job := range jobs {
		line := fmt.Sprintf("#%d | %s | %s", job.ID, job.Status, trimURL(job.URL))
		if l := modeLabel(job); l != "" {
			line += " | " + l
		}
		if job.Platform != "" {
			line += fmt.Sprintf(" | %s", job.Platform)
		}
		if job.Status == StatusCanceled && job.ErrorMessage != "" {
			line += fmt.Sprintf(" | %s", job.ErrorMessage)
		}
		if job.FinishedAt != nil {
			line += fmt.Sprintf(" | %s", job.FinishedAt.Format("Jan 02 15:04"))
		}
		lines = append(lines, line)
	}
	return joinLines(lines)
}

func FormatActiveJobs(jobs []Job) string {
	if len(jobs) == 0 {
		return "No active or pending jobs."
	}
	lines := []string{fmt.Sprintf("Active/pending jobs (%d):", len(jobs))}
	for _, job := range jobs {
		line := fmt.Sprintf("#%d | %s | user %d | %s", job.ID, job.Status, job.UserID, trimURL(job.URL))
		if l := modeLabel(job); l != "" {
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

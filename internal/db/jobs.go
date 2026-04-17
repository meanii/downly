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

type Job struct {
	ID              int64
	ChatID          int64
	UserID          int64
	URL             string
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

type QueueStats struct {
	PendingAhead int
	Active       int
	UserPending  int
}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	stmts := []string{
		`
		create table if not exists download_jobs (
			id bigserial primary key,
			chat_id bigint not null,
			user_id bigint not null default 0,
			url text not null,
			platform text not null default '',
			status text not null,
			priority integer not null default 0,
			output_path text not null default '',
			output_name text not null default '',
			error_message text not null default '',
			retry_count integer not null default 0,
			telegram_message_id bigint not null default 0,
			progress_text text not null default '',
			progress_percent integer not null default 0,
			created_at timestamptz not null default now(),
			started_at timestamptz,
			finished_at timestamptz
		);
		`,
		`alter table download_jobs add column if not exists progress_text text not null default '';`,
		`alter table download_jobs add column if not exists progress_percent integer not null default 0;`,
		`alter table download_jobs add column if not exists priority integer not null default 0;`,
		`create index if not exists idx_download_jobs_status_priority_created_at on download_jobs(status, priority desc, created_at);`,
		`
		create table if not exists banned_users (
			user_id bigint primary key,
			banned_at timestamptz not null default now(),
			reason text not null default ''
		);
		`,
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func InsertJob(ctx context.Context, pool *pgxpool.Pool, chatID, userID int64, url string, telegramMsgID int64, priority int) (int64, error) {
	var jobID int64
	err := pool.QueryRow(ctx, `
		insert into download_jobs (chat_id, user_id, url, status, priority, telegram_message_id, progress_text, progress_percent)
		values ($1, $2, $3, $4, $5, $6, $7, $8)
		returning id
	`, chatID, userID, url, StatusPending, priority, telegramMsgID, "Queued", 0).Scan(&jobID)
	return jobID, err
}

func ClaimJob(ctx context.Context, pool *pgxpool.Pool) (*Job, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		select id, chat_id, user_id, url, status, priority, telegram_message_id, progress_text, progress_percent, created_at, retry_count
		from download_jobs
		where status = $1
		order by priority desc, created_at asc
		for update skip locked
		limit 1
	`, StatusPending)

	var job Job
	if err := row.Scan(&job.ID, &job.ChatID, &job.UserID, &job.URL, &job.Status, &job.Priority, &job.TelegramMsgID, &job.ProgressText, &job.ProgressPercent, &job.CreatedAt, &job.RetryCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		update download_jobs
		set status = $2, started_at = now(), progress_text = $3, progress_percent = $4
		where id = $1
	`, job.ID, StatusProcessing, "Starting download", 1); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	job.Status = StatusProcessing
	job.ProgressText = "Starting download"
	job.ProgressPercent = 1
	return &job, nil
}

func MarkDone(ctx context.Context, pool *pgxpool.Pool, jobID int64, outputPath, outputName, platform string) error {
	_, err := pool.Exec(ctx, `
		update download_jobs
		set status = $2, output_path = $3, output_name = $4, platform = $5, error_message = '', progress_text = $6, progress_percent = $7, finished_at = now()
		where id = $1
	`, jobID, StatusDone, outputPath, outputName, platform, "Completed", 100)
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

func MarkCanceled(ctx context.Context, pool *pgxpool.Pool, jobID int64) error {
	_, err := pool.Exec(ctx, `
		update download_jobs
		set status = $2, progress_text = $3, finished_at = now()
		where id = $1
	`, jobID, StatusCanceled, "Canceled")
	return err
}

func CancelPendingJob(ctx context.Context, pool *pgxpool.Pool, jobID, userID int64) (bool, error) {
	cmd, err := pool.Exec(ctx, `
		update download_jobs
		set status = $3, progress_text = $4, finished_at = now()
		where id = $1 and user_id = $2 and status = $5
	`, jobID, userID, StatusCanceled, "Canceled", StatusPending)
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
	if err = pool.QueryRow(ctx, `select count(*) from download_jobs where user_id = $1 and status = $2`, userID, StatusPending).Scan(&queued); err != nil {
		return
	}
	if err = pool.QueryRow(ctx, `select count(*) from download_jobs where user_id = $1 and status = $2`, userID, StatusProcessing).Scan(&processing); err != nil {
		return
	}
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

func GetQueueStats(ctx context.Context, pool *pgxpool.Pool, jobID, userID int64) (*QueueStats, error) {
	stats := &QueueStats{}
	if err := pool.QueryRow(ctx, `
		select count(*)
		from download_jobs
		where status = $1 and (
			priority > (select priority from download_jobs where id = $2)
			or (priority = (select priority from download_jobs where id = $2) and created_at < (select created_at from download_jobs where id = $2))
		)
	`, StatusPending, jobID).Scan(&stats.PendingAhead); err != nil {
		return nil, err
	}
	if err := pool.QueryRow(ctx, `select count(*) from download_jobs where status = $1`, StatusProcessing).Scan(&stats.Active); err != nil {
		return nil, err
	}
	if err := pool.QueryRow(ctx, `select count(*) from download_jobs where user_id = $1 and status in ($2, $3)`, userID, StatusPending, StatusProcessing).Scan(&stats.UserPending); err != nil {
		return nil, err
	}
	return stats, nil
}

func GetUserJobs(ctx context.Context, pool *pgxpool.Pool, userID int64, limit int) ([]Job, error) {
	rows, err := pool.Query(ctx, `
		select id, chat_id, user_id, url, platform, status, priority, output_path, output_name, error_message, retry_count, telegram_message_id, progress_text, progress_percent, created_at, started_at, finished_at
		from download_jobs
		where user_id = $1
		order by created_at desc
		limit $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.ChatID, &job.UserID, &job.URL, &job.Platform, &job.Status, &job.Priority, &job.OutputPath, &job.OutputName, &job.ErrorMessage, &job.RetryCount, &job.TelegramMsgID, &job.ProgressText, &job.ProgressPercent, &job.CreatedAt, &job.StartedAt, &job.FinishedAt); err != nil {
			return nil, err
		}
		if job.Status == StatusPending {
			pos, err := pendingPosition(ctx, pool, job.ID)
			if err == nil {
				job.QueuePosition = pos
			}
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func pendingPosition(ctx context.Context, pool *pgxpool.Pool, jobID int64) (int, error) {
	var pos int
	err := pool.QueryRow(ctx, `
		select count(*) + 1
		from download_jobs
		where status = $1 and (
			priority > (select priority from download_jobs where id = $2)
			or (priority = (select priority from download_jobs where id = $2) and created_at < (select created_at from download_jobs where id = $2))
		)
	`, StatusPending, jobID).Scan(&pos)
	return pos, err
}

type BotStats struct {
	TotalJobs      int
	TotalDone      int
	TotalFailed    int
	TotalCanceled  int
	TotalPending   int
	TotalActive    int
	UniqueUsers    int
	TotalPlatforms int
}

type TopUser struct {
	UserID   int64
	JobCount int
}

type PlatformCount struct {
	Platform string
	Count    int
}

func GetBotStats(ctx context.Context, pool *pgxpool.Pool) (*BotStats, error) {
	stats := &BotStats{}
	err := pool.QueryRow(ctx, `
		select
			count(*),
			count(*) filter (where status = 'done'),
			count(*) filter (where status = 'failed'),
			count(*) filter (where status = 'canceled'),
			count(*) filter (where status = 'pending'),
			count(*) filter (where status = 'processing'),
			count(distinct user_id),
			count(distinct platform) filter (where platform != '')
		from download_jobs
	`).Scan(
		&stats.TotalJobs, &stats.TotalDone, &stats.TotalFailed,
		&stats.TotalCanceled, &stats.TotalPending, &stats.TotalActive,
		&stats.UniqueUsers, &stats.TotalPlatforms,
	)
	return stats, err
}

func GetTopUsers(ctx context.Context, pool *pgxpool.Pool, limit int) ([]TopUser, error) {
	rows, err := pool.Query(ctx, `
		select user_id, count(*) as job_count
		from download_jobs
		group by user_id
		order by job_count desc
		limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []TopUser
	for rows.Next() {
		var u TopUser
		if err := rows.Scan(&u.UserID, &u.JobCount); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func GetTopPlatforms(ctx context.Context, pool *pgxpool.Pool, limit int) ([]PlatformCount, error) {
	rows, err := pool.Query(ctx, `
		select platform, count(*) as cnt
		from download_jobs
		where platform != ''
		group by platform
		order by cnt desc
		limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var platforms []PlatformCount
	for rows.Next() {
		var p PlatformCount
		if err := rows.Scan(&p.Platform, &p.Count); err != nil {
			return nil, err
		}
		platforms = append(platforms, p)
	}
	return platforms, rows.Err()
}

func FormatBotStats(stats *BotStats, topUsers []TopUser, topPlatforms []PlatformCount) string {
	lines := []string{
		"Bot Statistics",
		"",
		fmt.Sprintf("Total jobs: %d", stats.TotalJobs),
		fmt.Sprintf("Completed: %d", stats.TotalDone),
		fmt.Sprintf("Failed: %d", stats.TotalFailed),
		fmt.Sprintf("Canceled: %d", stats.TotalCanceled),
		fmt.Sprintf("Pending: %d", stats.TotalPending),
		fmt.Sprintf("Active: %d", stats.TotalActive),
		fmt.Sprintf("Unique users: %d", stats.UniqueUsers),
	}

	if stats.TotalJobs > 0 {
		successRate := float64(stats.TotalDone) / float64(stats.TotalJobs) * 100
		lines = append(lines, fmt.Sprintf("Success rate: %.1f%%", successRate))
	}

	if len(topPlatforms) > 0 {
		lines = append(lines, "", "Top platforms:")
		for i, p := range topPlatforms {
			lines = append(lines, fmt.Sprintf("  %d. %s (%d)", i+1, p.Platform, p.Count))
		}
	}

	if len(topUsers) > 0 {
		lines = append(lines, "", "Top users:")
		for i, u := range topUsers {
			lines = append(lines, fmt.Sprintf("  %d. %d (%d jobs)", i+1, u.UserID, u.JobCount))
		}
	}

	return joinLines(lines)
}

func FormatUserQueueSummary(jobs []Job) string {
	if len(jobs) == 0 {
		return "You have no recent jobs. Send a URL to queue a download."
	}
	lines := []string{"Your recent jobs:"}
	for _, job := range jobs {
		line := fmt.Sprintf("#%d | %s | %s", job.ID, job.Status, trimURL(job.URL))
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

// --- User Preferences ---

func EnsurePreferencesTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		create table if not exists user_preferences (
			user_id bigint primary key,
			quality text not null default 'best',
			updated_at timestamptz not null default now()
		)
	`)
	return err
}

func SetUserQuality(ctx context.Context, pool *pgxpool.Pool, userID int64, quality string) error {
	_, err := pool.Exec(ctx, `
		insert into user_preferences (user_id, quality, updated_at) values ($1, $2, now())
		on conflict (user_id) do update set quality = $2, updated_at = now()
	`, userID, quality)
	return err
}

func GetUserQuality(ctx context.Context, pool *pgxpool.Pool, userID int64) (string, error) {
	var quality string
	err := pool.QueryRow(ctx, `select quality from user_preferences where user_id = $1`, userID).Scan(&quality)
	if err != nil {
		return "best", nil // default to best if no preference set
	}
	return quality, nil
}

// --- Ban / Unban ---

func BanUser(ctx context.Context, pool *pgxpool.Pool, userID int64, reason string) error {
	_, err := pool.Exec(ctx, `
		insert into banned_users (user_id, reason) values ($1, $2)
		on conflict (user_id) do update set banned_at = now(), reason = $2
	`, userID, reason)
	return err
}

func UnbanUser(ctx context.Context, pool *pgxpool.Pool, userID int64) (bool, error) {
	cmd, err := pool.Exec(ctx, `delete from banned_users where user_id = $1`, userID)
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() > 0, nil
}

func IsBanned(ctx context.Context, pool *pgxpool.Pool, userID int64) (bool, error) {
	var count int
	err := pool.QueryRow(ctx, `select count(*) from banned_users where user_id = $1`, userID).Scan(&count)
	return count > 0, err
}

// --- Broadcast ---

func GetAllChatIDs(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	rows, err := pool.Query(ctx, `select distinct chat_id from download_jobs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// --- History ---

func GetUserHistory(ctx context.Context, pool *pgxpool.Pool, userID int64, limit int) ([]Job, error) {
	rows, err := pool.Query(ctx, `
		select id, chat_id, user_id, url, platform, status, priority, output_path, output_name, error_message, retry_count, telegram_message_id, progress_text, progress_percent, created_at, started_at, finished_at
		from download_jobs
		where user_id = $1 and status in ('done', 'failed', 'canceled')
		order by created_at desc
		limit $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.ChatID, &job.UserID, &job.URL, &job.Platform, &job.Status, &job.Priority, &job.OutputPath, &job.OutputName, &job.ErrorMessage, &job.RetryCount, &job.TelegramMsgID, &job.ProgressText, &job.ProgressPercent, &job.CreatedAt, &job.StartedAt, &job.FinishedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func FormatUserHistory(jobs []Job) string {
	if len(jobs) == 0 {
		return "No download history yet."
	}
	lines := []string{"Your download history:"}
	for _, job := range jobs {
		status := string(job.Status)
		line := fmt.Sprintf("#%d | %s | %s", job.ID, status, trimURL(job.URL))
		if job.Platform != "" {
			line += fmt.Sprintf(" | %s", job.Platform)
		}
		if job.FinishedAt != nil {
			line += fmt.Sprintf(" | %s", job.FinishedAt.Format("Jan 02 15:04"))
		}
		lines = append(lines, line)
	}
	return joinLines(lines)
}

// --- Admin: Users ---

type UserInfo struct {
	UserID   int64
	JobCount int
	LastSeen time.Time
}

func GetAllUsers(ctx context.Context, pool *pgxpool.Pool, limit int) ([]UserInfo, error) {
	rows, err := pool.Query(ctx, `
		select user_id, count(*) as job_count, max(created_at) as last_seen
		from download_jobs
		where user_id > 0
		group by user_id
		order by last_seen desc
		limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []UserInfo
	for rows.Next() {
		var u UserInfo
		if err := rows.Scan(&u.UserID, &u.JobCount, &u.LastSeen); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func FormatUserList(users []UserInfo) string {
	if len(users) == 0 {
		return "No users found."
	}
	lines := []string{fmt.Sprintf("Users (%d):", len(users))}
	for i, u := range users {
		lines = append(lines, fmt.Sprintf("%d. %d | %d jobs | last: %s", i+1, u.UserID, u.JobCount, u.LastSeen.Format("Jan 02 15:04")))
	}
	return joinLines(lines)
}

// --- Admin: Jobs ---

func GetActiveJobs(ctx context.Context, pool *pgxpool.Pool, limit int) ([]Job, error) {
	rows, err := pool.Query(ctx, `
		select id, chat_id, user_id, url, platform, status, priority, output_path, output_name, error_message, retry_count, telegram_message_id, progress_text, progress_percent, created_at, started_at, finished_at
		from download_jobs
		where status in ('pending', 'processing')
		order by status desc, priority desc, created_at asc
		limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var job Job
		if err := rows.Scan(&job.ID, &job.ChatID, &job.UserID, &job.URL, &job.Platform, &job.Status, &job.Priority, &job.OutputPath, &job.OutputName, &job.ErrorMessage, &job.RetryCount, &job.TelegramMsgID, &job.ProgressText, &job.ProgressPercent, &job.CreatedAt, &job.StartedAt, &job.FinishedAt); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func FormatActiveJobs(jobs []Job) string {
	if len(jobs) == 0 {
		return "No active or pending jobs."
	}
	lines := []string{fmt.Sprintf("Active/pending jobs (%d):", len(jobs))}
	for _, job := range jobs {
		line := fmt.Sprintf("#%d | %s | user %d | %s", job.ID, job.Status, job.UserID, trimURL(job.URL))
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

// --- Daily Quota ---

func UserDailyJobCount(ctx context.Context, pool *pgxpool.Pool, userID int64) (int, error) {
	var count int
	err := pool.QueryRow(ctx, `
		select count(*) from download_jobs
		where user_id = $1 and created_at >= now() - interval '24 hours'
	`, userID).Scan(&count)
	return count, err
}

// --- Platform Health ---

type PlatformHealth struct {
	Platform    string
	Total       int
	Succeeded   int
	Failed      int
	SuccessRate float64
}

func GetPlatformHealth(ctx context.Context, pool *pgxpool.Pool, hours int, limit int) ([]PlatformHealth, error) {
	rows, err := pool.Query(ctx, `
		select
			platform,
			count(*) as total,
			count(*) filter (where status = 'done') as succeeded,
			count(*) filter (where status = 'failed') as failed
		from download_jobs
		where platform != '' and created_at >= now() - make_interval(hours := $1)
		group by platform
		order by total desc
		limit $2
	`, hours, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var platforms []PlatformHealth
	for rows.Next() {
		var p PlatformHealth
		if err := rows.Scan(&p.Platform, &p.Total, &p.Succeeded, &p.Failed); err != nil {
			return nil, err
		}
		if p.Total > 0 {
			p.SuccessRate = float64(p.Succeeded) / float64(p.Total) * 100
		}
		platforms = append(platforms, p)
	}
	return platforms, rows.Err()
}

func FormatPlatformHealth(platforms []PlatformHealth, hours int) string {
	if len(platforms) == 0 {
		return "No platform data available."
	}
	lines := []string{fmt.Sprintf("Platform Health (last %dh):", hours)}
	for _, p := range platforms {
		status := "OK"
		if p.SuccessRate < 50 {
			status = "FAILING"
		} else if p.SuccessRate < 80 {
			status = "DEGRADED"
		}
		lines = append(lines, fmt.Sprintf("  %s [%s] — %d/%d ok (%.0f%%)", p.Platform, status, p.Succeeded, p.Total, p.SuccessRate))
	}
	return joinLines(lines)
}

// --- Dead Job Reaper ---

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


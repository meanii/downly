package db

import (
	"context"
	"errors"
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
)

type Job struct {
	ID            int64
	ChatID        int64
	UserID        int64
	URL           string
	Platform      string
	Status        JobStatus
	OutputPath    string
	OutputName    string
	ErrorMessage  string
	RetryCount    int
	TelegramMsgID int64
	CreatedAt     time.Time
}

func EnsureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		create table if not exists download_jobs (
			id bigserial primary key,
			chat_id bigint not null,
			user_id bigint not null default 0,
			url text not null,
			platform text not null default '',
			status text not null,
			output_path text not null default '',
			output_name text not null default '',
			error_message text not null default '',
			retry_count integer not null default 0,
			telegram_message_id bigint not null default 0,
			created_at timestamptz not null default now(),
			started_at timestamptz,
			finished_at timestamptz
		);
		create index if not exists idx_download_jobs_status_created_at on download_jobs(status, created_at);
	`)
	return err
}

func InsertJob(ctx context.Context, pool *pgxpool.Pool, chatID, userID int64, url string, telegramMsgID int64) error {
	_, err := pool.Exec(ctx, `
		insert into download_jobs (chat_id, user_id, url, status, telegram_message_id)
		values ($1, $2, $3, $4, $5)
	`, chatID, userID, url, StatusPending, telegramMsgID)
	return err
}

func ClaimJob(ctx context.Context, pool *pgxpool.Pool) (*Job, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	row := tx.QueryRow(ctx, `
		select id, chat_id, user_id, url, status, telegram_message_id, created_at
		from download_jobs
		where status = $1
		order by created_at asc
		for update skip locked
		limit 1
	`, StatusPending)

	var job Job
	if err := row.Scan(&job.ID, &job.ChatID, &job.UserID, &job.URL, &job.Status, &job.TelegramMsgID, &job.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return nil, err
			}
			return nil, nil
		}
		return nil, err
	}

	if _, err := tx.Exec(ctx, `
		update download_jobs set status = $2, started_at = now() where id = $1
	`, job.ID, StatusProcessing); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	job.Status = StatusProcessing
	return &job, nil
}

func MarkDone(ctx context.Context, pool *pgxpool.Pool, jobID int64, outputPath, outputName, platform string) error {
	_, err := pool.Exec(ctx, `
		update download_jobs
		set status = $2, output_path = $3, output_name = $4, platform = $5, error_message = '', finished_at = now()
		where id = $1
	`, jobID, StatusDone, outputPath, outputName, platform)
	return err
}

func MarkFailed(ctx context.Context, pool *pgxpool.Pool, jobID int64, errMsg string) error {
	_, err := pool.Exec(ctx, `
		update download_jobs
		set status = $2, error_message = $3, retry_count = retry_count + 1, finished_at = now()
		where id = $1
	`, jobID, StatusFailed, errMsg)
	return err
}

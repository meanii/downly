package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EnqueueLimits caps how much a single user can have queued. Zero disables a limit.
type EnqueueLimits struct {
	MaxQueued  int // pending jobs at once
	DailyQuota int // jobs created in the last 24h
}

// LimitKind says which limit rejected an enqueue.
type LimitKind string

const (
	LimitQueue LimitKind = "queue"
	LimitDaily LimitKind = "daily"
)

// LimitError is returned by EnqueueJob when a user is over a limit.
type LimitError struct {
	Kind  LimitKind
	Count int
	Limit int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("%s limit reached (%d/%d)", e.Kind, e.Count, e.Limit)
}

// IsLimit reports whether err is a *LimitError.
func IsLimit(err error) (*LimitError, bool) {
	var le *LimitError
	ok := errors.As(err, &le)
	return le, ok
}

// userLockClass namespaces per-user advisory locks.
const userLockClass = 0x646c

func lockUser(ctx context.Context, tx pgx.Tx, userID int64) error {
	_, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1, hashtext(($2::bigint)::text))`, userLockClass, userID)
	return err
}

func userCounts(ctx context.Context, q pgx.Tx, userID int64) (pending, daily int, err error) {
	err = q.QueryRow(ctx, `
		select count(*) filter (where status = 'pending'),
		       count(*) filter (where created_at >= now() - interval '24 hours')
		from download_jobs where user_id = $1
	`, userID).Scan(&pending, &daily)
	return
}

func checkLimits(pending, daily int, lim EnqueueLimits) error {
	if lim.MaxQueued > 0 && pending >= lim.MaxQueued {
		return &LimitError{Kind: LimitQueue, Count: pending, Limit: lim.MaxQueued}
	}
	if lim.DailyQuota > 0 && daily >= lim.DailyQuota {
		return &LimitError{Kind: LimitDaily, Count: daily, Limit: lim.DailyQuota}
	}
	return nil
}

// EnqueueJob inserts a job if the user is within limits. The check and the
// insert run under a per-user lock, so concurrent messages cannot overshoot.
func EnqueueJob(ctx context.Context, pool *pgxpool.Pool, j NewJob, lim EnqueueLimits) (int64, error) {
	if j.Mode == "" {
		j.Mode = ModeVideo
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	if err := lockUser(ctx, tx, j.UserID); err != nil {
		return 0, err
	}
	pending, daily, err := userCounts(ctx, tx, j.UserID)
	if err != nil {
		return 0, err
	}
	if err := checkLimits(pending, daily, lim); err != nil {
		return 0, err
	}
	var id int64
	if err := tx.QueryRow(ctx, `
		insert into download_jobs (chat_id, user_id, url, mode, quality, status, priority, telegram_message_id, progress_text, progress_percent)
		values ($1, $2, $3, $4, $5, $6, $7, $8, 'Queued', 0)
		returning id
	`, j.ChatID, j.UserID, j.URL, j.Mode, j.Quality, StatusPending, j.Priority, j.TelegramMsgID).Scan(&id); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// QueueRoom returns how many more jobs the user may enqueue right now, or -1
// if no limit applies.
func QueueRoom(ctx context.Context, pool *pgxpool.Pool, userID int64, lim EnqueueLimits) (int, error) {
	var pending, daily int
	err := pool.QueryRow(ctx, `
		select count(*) filter (where status = 'pending'),
		       count(*) filter (where created_at >= now() - interval '24 hours')
		from download_jobs where user_id = $1
	`, userID).Scan(&pending, &daily)
	if err != nil {
		return 0, err
	}
	room := -1
	if lim.MaxQueued > 0 {
		room = max(lim.MaxQueued-pending, 0)
	}
	if lim.DailyQuota > 0 {
		d := max(lim.DailyQuota-daily, 0)
		if room < 0 || d < room {
			room = d
		}
	}
	return room, nil
}

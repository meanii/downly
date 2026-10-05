package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Subscription is a channel or playlist a chat follows.
type Subscription struct {
	ID       int64
	ChatID   int64
	UserID   int64
	URL      string
	Title    string
	Mode     JobMode
	Failures int
}

var (
	// ErrSubscriptionExists is returned when the chat already follows the URL.
	ErrSubscriptionExists = errors.New("already subscribed")
	// ErrSubscriptionLimit is returned when the chat follows too many feeds.
	ErrSubscriptionLimit = errors.New("subscription limit reached")
)

// AddSubscription follows url in chatID, marking seenIDs as already
// delivered so existing uploads aren't sent. The first check is due after
// interval.
func AddSubscription(ctx context.Context, pool *pgxpool.Pool, s Subscription, seenIDs []string, maxPerChat int, interval time.Duration) (int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize per chat so the limit holds under concurrent /follow.
	if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1, hashtext(($2::bigint)::text))`, userLockClass+1, s.ChatID); err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRow(ctx, `select count(*) from subscriptions where chat_id = $1`, s.ChatID).Scan(&n); err != nil {
		return 0, err
	}
	if maxPerChat > 0 && n >= maxPerChat {
		return 0, ErrSubscriptionLimit
	}
	if s.Mode == "" {
		s.Mode = ModeVideo
	}
	var id int64
	err = tx.QueryRow(ctx, `
		insert into subscriptions (chat_id, user_id, url, title, mode, next_check_at)
		values ($1, $2, $3, $4, $5, now() + make_interval(secs => $6))
		returning id
	`, s.ChatID, s.UserID, s.URL, s.Title, s.Mode, interval.Seconds()).Scan(&id)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return 0, ErrSubscriptionExists
	}
	if err != nil {
		return 0, err
	}
	if err := markSeen(ctx, tx, id, seenIDs); err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// ListSubscriptions returns a chat's subscriptions, oldest first.
func ListSubscriptions(ctx context.Context, pool *pgxpool.Pool, chatID int64) ([]Subscription, error) {
	rows, err := pool.Query(ctx, `
		select id, chat_id, user_id, url, title, mode, failures from subscriptions where chat_id = $1 order by id
	`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.ID, &s.ChatID, &s.UserID, &s.URL, &s.Title, &s.Mode, &s.Failures); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSubscription removes a subscription of chatID. ok is false if it
// doesn't exist or belongs to another chat.
func DeleteSubscription(ctx context.Context, pool *pgxpool.Pool, id, chatID int64) (bool, error) {
	cmd, err := pool.Exec(ctx, `delete from subscriptions where id = $1 and chat_id = $2`, id, chatID)
	if err != nil {
		return false, err
	}
	return cmd.RowsAffected() > 0, nil
}

// ClaimDueSubscriptions returns up to limit subscriptions due for a check
// and pushes their next check out by lease, so concurrent pollers (other
// instances) skip them.
func ClaimDueSubscriptions(ctx context.Context, pool *pgxpool.Pool, limit int, lease time.Duration) ([]Subscription, error) {
	rows, err := pool.Query(ctx, `
		update subscriptions s set next_check_at = now() + make_interval(secs => $2)
		from (
			select id from subscriptions where next_check_at <= now()
			order by next_check_at for update skip locked limit $1
		) due
		where s.id = due.id
		returning s.id, s.chat_id, s.user_id, s.url, s.title, s.mode, s.failures
	`, limit, lease.Seconds())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Subscription
	for rows.Next() {
		var s Subscription
		if err := rows.Scan(&s.ID, &s.ChatID, &s.UserID, &s.URL, &s.Title, &s.Mode, &s.Failures); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// UnseenEntries filters ids down to those not yet delivered for the
// subscription, preserving order.
func UnseenEntries(ctx context.Context, pool *pgxpool.Pool, subID int64, ids []string) ([]string, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := pool.Query(ctx, `select entry_id from subscription_seen where subscription_id = $1 and entry_id = any($2)`, subID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		seen[id] = true
	}
	var out []string
	for _, id := range ids {
		if !seen[id] {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// MarkSeen records entries as delivered.
func MarkSeen(ctx context.Context, pool *pgxpool.Pool, subID int64, ids []string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := markSeen(ctx, tx, subID, ids); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func markSeen(ctx context.Context, tx pgx.Tx, subID int64, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		insert into subscription_seen (subscription_id, entry_id)
		select $1, unnest($2::text[]) on conflict do nothing
	`, subID, ids)
	if err != nil {
		return err
	}
	// Keep the seen set bounded; feeds only list recent entries anyway.
	_, err = tx.Exec(ctx, `
		delete from subscription_seen where subscription_id = $1 and entry_id not in (
			select entry_id from subscription_seen where subscription_id = $1 order by seen_at desc limit 500
		)
	`, subID)
	return err
}

// RecordSubscriptionCheck stores the outcome of a check and schedules the
// next one after next.
func RecordSubscriptionCheck(ctx context.Context, pool *pgxpool.Pool, subID int64, ok bool, title string, next time.Duration) error {
	_, err := pool.Exec(ctx, `
		update subscriptions set
			last_checked_at = now(),
			next_check_at = now() + make_interval(secs => $3),
			failures = case when $2 then 0 else failures + 1 end,
			title = case when $4 <> '' then $4 else title end
		where id = $1
	`, subID, ok, next.Seconds(), title)
	return err
}

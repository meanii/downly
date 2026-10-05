package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TouchUser records that a user interacted with the bot. chatID should be
// the private chat with the user, or 0 if unknown (e.g. a group message);
// a known chat ID is never overwritten by 0.
func TouchUser(ctx context.Context, pool *pgxpool.Pool, userID, chatID int64, username, firstName string) error {
	if userID <= 0 {
		return nil
	}
	_, err := pool.Exec(ctx, `
		insert into users (user_id, chat_id, username, first_name)
		values ($1, $2, $3, $4)
		on conflict (user_id) do update set
			chat_id = case when excluded.chat_id <> 0 then excluded.chat_id else users.chat_id end,
			username = excluded.username,
			first_name = excluded.first_name,
			last_seen = now(),
			blocked_bot = false
	`, userID, chatID, username, firstName)
	return err
}

// MarkUserBlocked flags a user whose private chat rejected our message.
func MarkUserBlocked(ctx context.Context, pool *pgxpool.Pool, chatID int64) error {
	_, err := pool.Exec(ctx, `update users set blocked_bot = true where chat_id = $1`, chatID)
	return err
}

// GetAllChatIDs returns the private chats of users who have not blocked the bot.
func GetAllChatIDs(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	rows, err := pool.Query(ctx, `
		select chat_id from users
		where chat_id <> 0 and not blocked_bot
		and user_id not in (select user_id from banned_users)
		order by user_id
	`)
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

// --- User Preferences ---

func SetUserQuality(ctx context.Context, pool *pgxpool.Pool, userID int64, quality string) error {
	_, err := pool.Exec(ctx, `
		insert into user_preferences (user_id, quality, updated_at) values ($1, $2, now())
		on conflict (user_id) do update set quality = $2, updated_at = now()
	`, userID, quality)
	return err
}

// GetUserQuality returns the user's preferred quality, or "best" if unset.
func GetUserQuality(ctx context.Context, pool *pgxpool.Pool, userID int64) (string, error) {
	var quality string
	err := pool.QueryRow(ctx, `select quality from user_preferences where user_id = $1`, userID).Scan(&quality)
	if errors.Is(err, pgx.ErrNoRows) {
		return "best", nil
	}
	if err != nil {
		return "best", err
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
	var banned bool
	err := pool.QueryRow(ctx, `select exists(select 1 from banned_users where user_id = $1)`, userID).Scan(&banned)
	return banned, err
}

// --- Admin: Users ---

type UserInfo struct {
	UserID   int64
	Username string
	JobCount int
	LastSeen time.Time
}

// GetAllUsers lists the most recently active users with their lifetime job
// counts (including jobs already pruned into job_stats_daily).
func GetAllUsers(ctx context.Context, pool *pgxpool.Pool, limit int) ([]UserInfo, error) {
	rows, err := pool.Query(ctx, `
		select u.user_id, u.username,
			coalesce((select count(*) from download_jobs d where d.user_id = u.user_id), 0)
			+ coalesce((select sum(jobs) from job_stats_daily s where s.user_id = u.user_id), 0),
			u.last_seen
		from users u
		order by u.last_seen desc
		limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []UserInfo
	for rows.Next() {
		var u UserInfo
		if err := rows.Scan(&u.UserID, &u.Username, &u.JobCount, &u.LastSeen); err != nil {
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
		name := ""
		if u.Username != "" {
			name = " (@" + u.Username + ")"
		}
		lines = append(lines, fmt.Sprintf("%d. %d%s | %d jobs | last: %s", i+1, u.UserID, name, u.JobCount, u.LastSeen.Format("Jan 02 15:04")))
	}
	return joinLines(lines)
}

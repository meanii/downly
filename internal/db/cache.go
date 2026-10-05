package db

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/media"
)

// CacheEntry is media already on Telegram's servers.
type CacheEntry struct {
	Key   string
	Items []media.Item
	media.Meta
}

// GetCache looks up a cache entry. ok is false on a miss.
func GetCache(ctx context.Context, pool *pgxpool.Pool, key string) (*CacheEntry, bool, error) {
	e := &CacheEntry{Key: key}
	var items []byte
	err := pool.QueryRow(ctx, `
		select items, title, performer, platform, duration, size_bytes from media_cache where cache_key = $1
	`, key).Scan(&items, &e.Title, &e.Performer, &e.Platform, &e.Duration, &e.SizeBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := json.Unmarshal(items, &e.Items); err != nil || len(e.Items) == 0 {
		// A broken entry is as good as a miss; drop it.
		_ = DeleteCache(ctx, pool, key)
		return nil, false, nil
	}
	return e, true, nil
}

// PutCache stores or replaces an entry.
func PutCache(ctx context.Context, pool *pgxpool.Pool, e *CacheEntry) error {
	if e.Key == "" || len(e.Items) == 0 {
		return nil
	}
	items, err := json.Marshal(e.Items)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		insert into media_cache (cache_key, items, title, performer, platform, duration, size_bytes)
		values ($1, $2, $3, $4, $5, $6, $7)
		on conflict (cache_key) do update set items = excluded.items, title = excluded.title,
			performer = excluded.performer, platform = excluded.platform, duration = excluded.duration,
			size_bytes = excluded.size_bytes, created_at = now(), last_used_at = now()
	`, e.Key, items, e.Title, e.Performer, e.Platform, e.Duration, e.SizeBytes)
	return err
}

// TouchCache records a cache hit.
func TouchCache(ctx context.Context, pool *pgxpool.Pool, key string) error {
	_, err := pool.Exec(ctx, `update media_cache set hits = hits + 1, last_used_at = now() where cache_key = $1`, key)
	return err
}

// DeleteCache removes an entry, e.g. after Telegram rejected its file ID.
func DeleteCache(ctx context.Context, pool *pgxpool.Pool, key string) error {
	_, err := pool.Exec(ctx, `delete from media_cache where cache_key = $1`, key)
	return err
}

// PruneCache drops entries unused for longer than maxAge.
func PruneCache(ctx context.Context, pool *pgxpool.Pool, maxAge time.Duration) (int64, error) {
	cmd, err := pool.Exec(ctx, `delete from media_cache where last_used_at < now() - make_interval(secs => $1)`, maxAge.Seconds())
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}

// InsertCachedJob records a delivery served from the cache as a finished
// job, so history and stats include it.
func InsertCachedJob(ctx context.Context, pool *pgxpool.Pool, j NewJob, e *CacheEntry) (int64, error) {
	if j.Mode == "" {
		j.Mode = ModeVideo
	}
	var id int64
	err := pool.QueryRow(ctx, `
		insert into download_jobs (chat_id, user_id, url, mode, quality, status, priority, telegram_message_id,
			progress_text, progress_percent, cache_key, cached, platform, output_name, file_size_bytes, started_at, finished_at,
			reply_to_message_id, clip_start, clip_end)
		values ($1, $2, $3, $4, $5, $6, 0, $7, 'Completed', 100, $8, true, $9, $10, $11, now(), now(), $12, $13, $14)
		returning id
	`, j.ChatID, j.UserID, j.URL, j.Mode, j.Quality, StatusDone, j.TelegramMsgID, e.Key, e.Platform, e.Title, e.SizeBytes,
		j.ReplyTo, j.ClipStart, j.ClipEnd).Scan(&id)
	return id, err
}

// CacheStats summarizes cache effectiveness for /stats.
type CacheStats struct {
	Entries int
	Hits    int
}

func GetCacheStats(ctx context.Context, pool *pgxpool.Pool) (CacheStats, error) {
	var s CacheStats
	err := pool.QueryRow(ctx, `select count(*), coalesce(sum(hits), 0) from media_cache`).Scan(&s.Entries, &s.Hits)
	return s, err
}

// GetUserJob returns a job if it belongs to userID.
func GetUserJob(ctx context.Context, pool *pgxpool.Pool, jobID, userID int64) (*Job, error) {
	job, err := scanJob(pool.QueryRow(ctx, `select `+jobColumns+` from download_jobs where id = $1 and user_id = $2`, jobID, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

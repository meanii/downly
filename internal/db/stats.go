package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// allJobsSQL is every job ever seen: live rows plus the aggregates of pruned
// ones. Columns: user_id, platform, status, jobs, bytes, at.
const allJobsSQL = `(
	select user_id, platform, status, 1::bigint as jobs, file_size_bytes as bytes, coalesce(finished_at, created_at) as at
	from download_jobs
	union all
	select user_id, platform, status, jobs, bytes, last_at as at
	from job_stats_daily
)`

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
			coalesce(sum(jobs), 0),
			coalesce(sum(jobs) filter (where status = 'done'), 0),
			coalesce(sum(jobs) filter (where status = 'failed'), 0),
			coalesce(sum(jobs) filter (where status = 'canceled'), 0),
			coalesce(sum(jobs) filter (where status = 'pending'), 0),
			coalesce(sum(jobs) filter (where status = 'processing'), 0),
			count(distinct user_id),
			count(distinct platform) filter (where platform != '')
		from `+allJobsSQL+` j
	`).Scan(
		&stats.TotalJobs, &stats.TotalDone, &stats.TotalFailed,
		&stats.TotalCanceled, &stats.TotalPending, &stats.TotalActive,
		&stats.UniqueUsers, &stats.TotalPlatforms,
	)
	return stats, err
}

func GetTopUsers(ctx context.Context, pool *pgxpool.Pool, limit int) ([]TopUser, error) {
	rows, err := pool.Query(ctx, `
		select user_id, sum(jobs) as job_count
		from `+allJobsSQL+` j
		group by user_id
		order by job_count desc, user_id
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
		select platform, sum(jobs) as cnt
		from `+allJobsSQL+` j
		where platform != ''
		group by platform
		order by cnt desc, platform
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

// --- User Bandwidth ---

type UserBandwidth struct {
	UserID     int64
	TotalBytes int64
	JobCount   int
	AvgBytes   int64
	Platform   string // most recently used platform
	LastSeenAt time.Time
}

func GetUserBandwidth(ctx context.Context, pool *pgxpool.Pool, limit int) ([]UserBandwidth, error) {
	rows, err := pool.Query(ctx, `
		select
			user_id,
			sum(bytes)::bigint as total_bytes,
			sum(jobs)::bigint as job_count,
			(sum(bytes) / greatest(sum(jobs), 1))::bigint as avg_bytes,
			(array_agg(platform order by at desc))[1] as most_recent_platform,
			max(at) as last_seen
		from `+allJobsSQL+` j
		where user_id > 0 and status = 'done'
		group by user_id
		order by total_bytes desc
		limit $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []UserBandwidth
	for rows.Next() {
		var u UserBandwidth
		if err := rows.Scan(&u.UserID, &u.TotalBytes, &u.JobCount, &u.AvgBytes, &u.Platform, &u.LastSeenAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func FormatUserBandwidth(users []UserBandwidth) string {
	if len(users) == 0 {
		return "No bandwidth data available."
	}
	lines := []string{fmt.Sprintf("User Bandwidth Report (Top %d):", len(users))}
	lines = append(lines, "")
	totalBytes := int64(0)
	for i, u := range users {
		totalBytes += u.TotalBytes
		mb := float64(u.TotalBytes) / 1024.0 / 1024.0
		avgMB := float64(u.AvgBytes) / 1024.0 / 1024.0
		lines = append(lines, fmt.Sprintf("%d. User %d | %.1fMB in %d jobs | avg %.1fMB", i+1, u.UserID, mb, u.JobCount, avgMB))
		if u.Platform != "" && u.Platform != "unknown" {
			lines = append(lines, fmt.Sprintf("   Platform: %s | Last: %s", u.Platform, u.LastSeenAt.Format("Jan 02 15:04")))
		}
	}
	lines = append(lines, "")
	totalMB := float64(totalBytes) / 1024.0 / 1024.0
	lines = append(lines, fmt.Sprintf("Total (top %d users): %.1fMB", len(users), totalMB))
	return joinLines(lines)
}

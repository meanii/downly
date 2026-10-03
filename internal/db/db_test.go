package db_test

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/dbtest"
)

func insert(t *testing.T, pool *pgxpool.Pool, j db.NewJob) int64 {
	t.Helper()
	if j.URL == "" {
		j.URL = "https://example.com/v"
	}
	id, err := db.InsertJob(context.Background(), pool, j)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	return id
}

func TestInsertAndClaimRoundTripsModeAndQuality(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	id := insert(t, pool, db.NewJob{ChatID: 1, UserID: 1, Mode: db.ModeAudio, Quality: "q720", TelegramMsgID: 9})

	job, err := db.ClaimJob(ctx, pool)
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	if job.ID != id || job.Mode != db.ModeAudio || job.Quality != "q720" || job.TelegramMsgID != 9 {
		t.Fatalf("claimed %+v", job)
	}
	if job.Status != db.StatusProcessing || job.StartedAt == nil {
		t.Fatalf("claimed job not marked processing: %+v", job)
	}
	if again, err := db.ClaimJob(ctx, pool); err != nil || again != nil {
		t.Fatalf("queue should be empty, got %v %v", again, err)
	}
}

func TestClaimOrdersByPriorityThenAge(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	first := insert(t, pool, db.NewJob{ChatID: 1, UserID: 1})
	second := insert(t, pool, db.NewJob{ChatID: 1, UserID: 1})
	urgent := insert(t, pool, db.NewJob{ChatID: 1, UserID: 1, Priority: 10})

	for _, want := range []int64{urgent, first, second} {
		job, err := db.ClaimJob(ctx, pool)
		if err != nil || job == nil || job.ID != want {
			t.Fatalf("claimed %v (%v), want %d", job, err, want)
		}
	}
}

func TestConcurrentClaimsNeverDoubleClaim(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	const jobs = 40
	for i := 0; i < jobs; i++ {
		insert(t, pool, db.NewJob{ChatID: 1, UserID: int64(i)})
	}
	var mu sync.Mutex
	seen := map[int64]bool{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, err := db.ClaimJob(ctx, pool)
				if err != nil {
					t.Error(err)
					return
				}
				if job == nil {
					return
				}
				mu.Lock()
				if seen[job.ID] {
					t.Errorf("job %d claimed twice", job.ID)
				}
				seen[job.ID] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != jobs {
		t.Fatalf("claimed %d jobs, want %d", len(seen), jobs)
	}
}

func TestQueueStatsAndPositions(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	insert(t, pool, db.NewJob{ChatID: 1, UserID: 1})
	insert(t, pool, db.NewJob{ChatID: 2, UserID: 2})
	mine := insert(t, pool, db.NewJob{ChatID: 3, UserID: 3})

	st, err := db.GetQueueStats(ctx, pool, mine, 3)
	if err != nil {
		t.Fatal(err)
	}
	if st.PendingAhead != 2 || st.UserPending != 1 || st.Active != 0 {
		t.Fatalf("stats = %+v", st)
	}
	jobs, err := db.GetUserJobs(ctx, pool, 3, 10)
	if err != nil || len(jobs) != 1 || jobs[0].QueuePosition != 3 {
		t.Fatalf("jobs = %+v err=%v", jobs, err)
	}
	q, p, err := db.UserActiveCounts(ctx, pool, 3)
	if err != nil || q != 1 || p != 0 {
		t.Fatalf("active counts q=%d p=%d err=%v", q, p, err)
	}
}

// finishJob claims the next job and marks it done/failed, then backdates it.
func finishJob(t *testing.T, pool *pgxpool.Pool, done bool, platform string, size int64, daysAgo int) {
	t.Helper()
	ctx := context.Background()
	job, err := db.ClaimJob(ctx, pool)
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	if done {
		err = db.MarkDone(ctx, pool, job.ID, "/x", "x.mp4", platform, size)
	} else {
		err = db.MarkFailed(ctx, pool, job.ID, "boom")
		_, _ = pool.Exec(ctx, `update download_jobs set platform = $2 where id = $1`, job.ID, platform)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update download_jobs set finished_at = now() - make_interval(days => $2), created_at = now() - make_interval(days => $2) where id = $1`, job.ID, daysAgo); err != nil {
		t.Fatal(err)
	}
}

func TestPruneKeepsStatsIntact(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()

	// user 1: two old youtube downloads + one recent; user 2: one old failure.
	insert(t, pool, db.NewJob{ChatID: 1, UserID: 1})
	finishJob(t, pool, true, "youtube", 100, 10)
	insert(t, pool, db.NewJob{ChatID: 1, UserID: 1})
	finishJob(t, pool, true, "youtube", 300, 9)
	insert(t, pool, db.NewJob{ChatID: 1, UserID: 1})
	finishJob(t, pool, true, "instagram", 50, 0)
	insert(t, pool, db.NewJob{ChatID: 2, UserID: 2})
	finishJob(t, pool, false, "tiktok", 0, 10)
	insert(t, pool, db.NewJob{ChatID: 2, UserID: 2}) // still pending

	before, err := db.GetBotStats(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	bwBefore, _ := db.GetUserBandwidth(ctx, pool, 10)

	n, err := db.PruneJobs(ctx, pool, 72)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("pruned %d, want 3", n)
	}
	var live int
	_ = pool.QueryRow(ctx, `select count(*) from download_jobs`).Scan(&live)
	if live != 2 {
		t.Fatalf("live rows = %d, want 2", live)
	}

	after, err := db.GetBotStats(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if *after != *before {
		t.Fatalf("stats changed across prune:\nbefore %+v\nafter  %+v", before, after)
	}
	if after.TotalJobs != 5 || after.TotalDone != 3 || after.TotalFailed != 1 || after.TotalPending != 1 || after.UniqueUsers != 2 {
		t.Fatalf("unexpected stats %+v", after)
	}

	bwAfter, err := db.GetUserBandwidth(ctx, pool, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(bwAfter) != 1 || len(bwBefore) != 1 {
		t.Fatalf("bandwidth rows before=%d after=%d", len(bwBefore), len(bwAfter))
	}
	if bwAfter[0].TotalBytes != 450 || bwAfter[0].JobCount != 3 || bwAfter[0].AvgBytes != 150 {
		t.Fatalf("bandwidth after prune = %+v", bwAfter[0])
	}
	if bwAfter[0].Platform != "instagram" {
		t.Fatalf("most recent platform = %q, want instagram", bwAfter[0].Platform)
	}

	top, _ := db.GetTopPlatforms(ctx, pool, 5)
	if len(top) == 0 || top[0].Platform != "youtube" || top[0].Count != 2 {
		t.Fatalf("top platforms = %+v", top)
	}
	users, _ := db.GetTopUsers(ctx, pool, 5)
	if len(users) == 0 || users[0].UserID != 1 || users[0].JobCount != 3 {
		t.Fatalf("top users = %+v", users)
	}

	// A second prune is a no-op and must not double count.
	if n, _ := db.PruneJobs(ctx, pool, 72); n != 0 {
		t.Fatalf("second prune removed %d", n)
	}
	again, _ := db.GetBotStats(ctx, pool)
	if *again != *after {
		t.Fatalf("stats changed on no-op prune: %+v", again)
	}
}

func TestUsersTracking(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()

	if err := db.TouchUser(ctx, pool, 5, 5, "alice", "Alice"); err != nil {
		t.Fatal(err)
	}
	// A later group/inline interaction must not erase the private chat.
	if err := db.TouchUser(ctx, pool, 5, 0, "alice2", "Alice"); err != nil {
		t.Fatal(err)
	}
	_ = db.TouchUser(ctx, pool, 6, 6, "", "Bob")
	_ = db.TouchUser(ctx, pool, 7, 7, "", "Carol")
	_ = db.TouchUser(ctx, pool, 8, 0, "", "InlineOnly")

	if err := db.MarkUserBlocked(ctx, pool, 6); err != nil {
		t.Fatal(err)
	}
	if err := db.BanUser(ctx, pool, 7, "spam"); err != nil {
		t.Fatal(err)
	}

	ids, err := db.GetAllChatIDs(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != 5 {
		t.Fatalf("broadcast targets = %v, want [5]", ids)
	}

	// Contacting the bot again clears the blocked flag.
	_ = db.TouchUser(ctx, pool, 6, 6, "", "Bob")
	ids, _ = db.GetAllChatIDs(ctx, pool)
	if len(ids) != 2 {
		t.Fatalf("after unblock targets = %v", ids)
	}

	users, err := db.GetAllUsers(ctx, pool, 10)
	if err != nil || len(users) != 4 {
		t.Fatalf("users = %+v err=%v", users, err)
	}
	var alice db.UserInfo
	for _, u := range users {
		if u.UserID == 5 {
			alice = u
		}
	}
	if alice.Username != "alice2" {
		t.Fatalf("username not updated: %+v", alice)
	}
}

func TestPreferencesAndBans(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	if q, err := db.GetUserQuality(ctx, pool, 1); err != nil || q != "best" {
		t.Fatalf("default quality = %q %v", q, err)
	}
	_ = db.SetUserQuality(ctx, pool, 1, "q720")
	if q, _ := db.GetUserQuality(ctx, pool, 1); q != "q720" {
		t.Fatalf("quality = %q", q)
	}
	if banned, _ := db.IsBanned(ctx, pool, 1); banned {
		t.Fatal("not banned yet")
	}
	_ = db.BanUser(ctx, pool, 1, "")
	if banned, _ := db.IsBanned(ctx, pool, 1); !banned {
		t.Fatal("should be banned")
	}
	if ok, _ := db.UnbanUser(ctx, pool, 1); !ok {
		t.Fatal("unban should report removal")
	}
}

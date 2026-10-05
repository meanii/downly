package db_test

import (
	"context"
	"sync"
	"testing"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/dbtest"
)

func TestEnqueueJobLimitsHoldUnderConcurrency(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	lim := db.EnqueueLimits{MaxQueued: 3}

	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, limited := 0, 0
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := db.EnqueueJob(ctx, pool, db.NewJob{ChatID: 1, UserID: 1, URL: "https://a.com"}, lim)
			mu.Lock()
			defer mu.Unlock()
			if _, isLimit := db.IsLimit(err); isLimit {
				limited++
			} else if err != nil {
				t.Error(err)
			} else {
				ok++
			}
		}()
	}
	wg.Wait()
	if ok != 3 || limited != 17 {
		t.Fatalf("ok=%d limited=%d, want 3/17", ok, limited)
	}
}

func TestEnqueueJobDailyQuota(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	lim := db.EnqueueLimits{MaxQueued: 10, DailyQuota: 2}
	for i := 0; i < 2; i++ {
		if _, err := db.EnqueueJob(ctx, pool, db.NewJob{ChatID: 1, UserID: 1, URL: "https://a.com"}, lim); err != nil {
			t.Fatal(err)
		}
	}
	_, err := db.EnqueueJob(ctx, pool, db.NewJob{ChatID: 1, UserID: 1, URL: "https://a.com"}, lim)
	le, isLimit := db.IsLimit(err)
	if !isLimit || le.Kind != db.LimitDaily || le.Count != 2 {
		t.Fatalf("err = %v", err)
	}
	// Other users are unaffected.
	if _, err := db.EnqueueJob(ctx, pool, db.NewJob{ChatID: 2, UserID: 2, URL: "https://a.com"}, lim); err != nil {
		t.Fatal(err)
	}
}

func TestQueueRoom(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	if room, _ := db.QueueRoom(ctx, pool, 1, db.EnqueueLimits{}); room != -1 {
		t.Fatalf("unlimited room = %d", room)
	}
	insert(t, pool, db.NewJob{ChatID: 1, UserID: 1})
	if room, _ := db.QueueRoom(ctx, pool, 1, db.EnqueueLimits{MaxQueued: 5}); room != 4 {
		t.Fatalf("room = %d, want 4", room)
	}
	if room, _ := db.QueueRoom(ctx, pool, 1, db.EnqueueLimits{MaxQueued: 5, DailyQuota: 3}); room != 2 {
		t.Fatalf("room = %d, want 2 (daily is tighter)", room)
	}
}

func TestClaimJobLimitedPerUser(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	a1 := insert(t, pool, db.NewJob{ChatID: 1, UserID: 1})
	insert(t, pool, db.NewJob{ChatID: 1, UserID: 1})
	b1 := insert(t, pool, db.NewJob{ChatID: 2, UserID: 2})

	j, _ := db.ClaimJobLimited(ctx, pool, "w", 1)
	if j == nil || j.ID != a1 {
		t.Fatalf("first claim = %v", j)
	}
	// User 1 is at their cap, so user 2's job is next even though it is newer.
	j, _ = db.ClaimJobLimited(ctx, pool, "w", 1)
	if j == nil || j.ID != b1 {
		t.Fatalf("second claim = %v, want user 2's job", j)
	}
	if j, _ = db.ClaimJobLimited(ctx, pool, "w", 1); j != nil {
		t.Fatalf("user 1's second job must wait, got %d", j.ID)
	}
	if j, _ = db.ClaimJobLimited(ctx, pool, "w", 0); j == nil {
		t.Fatal("no cap means the job is claimable")
	}
}

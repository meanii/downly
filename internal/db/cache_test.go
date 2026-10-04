package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/dbtest"
	"github.com/meanii/downly/internal/media"
)

func TestCacheRoundTrip(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	if _, ok, err := db.GetCache(ctx, pool, "k"); ok || err != nil {
		t.Fatalf("miss expected: %v %v", ok, err)
	}
	e := &db.CacheEntry{Key: "k", Items: []media.Item{{Kind: media.Video, FileID: "f1"}, {Kind: media.Photo, FileID: "f2"}},
		Meta: media.Meta{Title: "T", Performer: "P", Platform: "youtube", Duration: 9, SizeBytes: 100}}
	if err := db.PutCache(ctx, pool, e); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.GetCache(ctx, pool, "k")
	if err != nil || !ok || len(got.Items) != 2 || got.Items[1].FileID != "f2" || got.Title != "T" || got.Performer != "P" || got.Duration != 9 {
		t.Fatalf("got %+v %v %v", got, ok, err)
	}
	_ = db.TouchCache(ctx, pool, "k")
	_ = db.TouchCache(ctx, pool, "k")
	if s, _ := db.GetCacheStats(ctx, pool); s.Entries != 1 || s.Hits != 2 {
		t.Fatalf("stats = %+v", s)
	}
	_ = db.DeleteCache(ctx, pool, "k")
	if _, ok, _ := db.GetCache(ctx, pool, "k"); ok {
		t.Fatal("delete failed")
	}
}

func TestCacheBrokenEntryIsAMiss(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `insert into media_cache (cache_key, items) values ('bad', '[]')`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := db.GetCache(ctx, pool, "bad"); ok || err != nil {
		t.Fatalf("broken entry should be a miss: %v %v", ok, err)
	}
	var n int
	_ = pool.QueryRow(ctx, `select count(*) from media_cache`).Scan(&n)
	if n != 0 {
		t.Fatal("broken entry should be dropped")
	}
}

func TestPruneCache(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	for _, k := range []string{"old", "new"} {
		_ = db.PutCache(ctx, pool, &db.CacheEntry{Key: k, Items: []media.Item{{Kind: media.Video, FileID: k}}})
	}
	_, _ = pool.Exec(ctx, `update media_cache set last_used_at = now() - interval '90 days' where cache_key = 'old'`)
	n, err := db.PruneCache(ctx, pool, 60*24*time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("pruned %d (%v)", n, err)
	}
	if _, ok, _ := db.GetCache(ctx, pool, "new"); !ok {
		t.Fatal("recent entry pruned")
	}
}

func TestCachedJobsDoNotCountTowardsDailyQuota(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	e := &db.CacheEntry{Key: "k", Items: []media.Item{{Kind: media.Video, FileID: "f"}}}
	for i := 0; i < 3; i++ {
		if _, err := db.InsertCachedJob(ctx, pool, db.NewJob{ChatID: 1, UserID: 1, URL: "https://a.com"}, e); err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := db.UserDailyJobCount(ctx, pool, 1); n != 0 {
		t.Fatalf("daily count = %d", n)
	}
	if _, err := db.EnqueueJob(ctx, pool, db.NewJob{ChatID: 1, UserID: 1, URL: "https://a.com"}, db.EnqueueLimits{DailyQuota: 1}); err != nil {
		t.Fatalf("quota should be untouched by cached jobs: %v", err)
	}
	stats, _ := db.GetBotStats(ctx, pool)
	if stats.TotalDone != 3 {
		t.Fatalf("cached deliveries should count as done in stats: %+v", stats)
	}
}

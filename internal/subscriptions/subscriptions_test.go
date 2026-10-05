package subscriptions

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/dbtest"
	"github.com/meanii/downly/internal/downloader"
)

func TestFeedURL(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/@chan":           "https://www.youtube.com/@chan/videos",
		"https://youtube.com/@chan/":              "https://youtube.com/@chan/videos",
		"https://www.youtube.com/@chan/shorts":    "https://www.youtube.com/@chan/shorts",
		"https://www.youtube.com/channel/UC123":   "https://www.youtube.com/channel/UC123/videos",
		"https://www.youtube.com/playlist?list=1": "https://www.youtube.com/playlist?list=1",
		"https://www.youtube.com/watch?v=x":       "https://www.youtube.com/watch?v=x",
		"https://vimeo.com/channels/staffpicks":   "https://vimeo.com/channels/staffpicks",
	}
	for in, want := range cases {
		if got := FeedURL(in); got != want {
			t.Errorf("FeedURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBackoff(t *testing.T) {
	p := &Poller{Interval: time.Hour}
	if p.backoff(1) != time.Hour || p.backoff(2) != 2*time.Hour || p.backoff(3) != 4*time.Hour {
		t.Fatal("backoff should double")
	}
	if p.backoff(20) != 24*time.Hour {
		t.Fatal("backoff should cap at a day")
	}
	if j := p.jittered(); j < time.Hour || j > time.Hour+6*time.Minute+time.Second {
		t.Fatalf("jitter out of range: %v", j)
	}
}

type fakeFetcher struct {
	entries []downloader.PlaylistEntry
	err     error
	calls   int
}

func (f *fakeFetcher) FetchPlaylist(context.Context, string, int) ([]downloader.PlaylistEntry, string, error) {
	f.calls++
	return f.entries, "Feed", f.err
}

func entries(ids ...string) []downloader.PlaylistEntry {
	var out []downloader.PlaylistEntry
	for _, id := range ids {
		out = append(out, downloader.PlaylistEntry{ID: id, URL: "https://8.8.8.8/v/" + id})
	}
	return out
}

func TestPollerFailureBackoffAndLease(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	f := &fakeFetcher{err: errors.New("boom")}
	p := &Poller{Pool: pool, Fetcher: f, Interval: time.Hour, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	id, err := db.AddSubscription(ctx, pool, db.Subscription{ChatID: 1, UserID: 1, URL: "https://8.8.8.8/c"}, nil, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := p.CheckDue(ctx); n != 1 {
		t.Fatalf("checked %d", n)
	}
	var failures int
	var dueIn float64
	_ = pool.QueryRow(ctx, `select failures, extract(epoch from next_check_at - now()) from subscriptions where id = $1`, id).Scan(&failures, &dueIn)
	if failures != 1 || dueIn < 3500 {
		t.Fatalf("failures=%d due in %.0fs", failures, dueIn)
	}
	// Not due again yet.
	if n, _ := p.CheckDue(ctx); n != 0 || f.calls != 1 {
		t.Fatalf("rechecked too early (n=%d calls=%d)", n, f.calls)
	}

	// Recovery resets the failure count.
	f.err, f.entries = nil, entries("a")
	_, _ = pool.Exec(ctx, `update subscriptions set next_check_at = now()`)
	_, _ = p.CheckDue(ctx)
	_ = pool.QueryRow(ctx, `select failures from subscriptions where id = $1`, id).Scan(&failures)
	if failures != 0 {
		t.Fatalf("failures after success = %d", failures)
	}
}

func TestAddSubscriptionLimits(t *testing.T) {
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	add := func(url string) error {
		_, err := db.AddSubscription(ctx, pool, db.Subscription{ChatID: 9, UserID: 9, URL: url}, []string{"seen"}, 2, time.Hour)
		return err
	}
	if err := add("https://a"); err != nil {
		t.Fatal(err)
	}
	if err := add("https://a"); !errors.Is(err, db.ErrSubscriptionExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := add("https://b"); err != nil {
		t.Fatal(err)
	}
	if err := add("https://c"); !errors.Is(err, db.ErrSubscriptionLimit) {
		t.Fatalf("limit: %v", err)
	}
	subs, _ := db.ListSubscriptions(ctx, pool, 9)
	unseen, _ := db.UnseenEntries(ctx, pool, subs[0].ID, []string{"seen", "new"})
	if len(unseen) != 1 || unseen[0] != "new" {
		t.Fatalf("unseen = %v", unseen)
	}
	if ok, _ := db.DeleteSubscription(ctx, pool, subs[0].ID, 10); ok {
		t.Fatal("deleted from the wrong chat")
	}
}

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meanii/downly/internal/db"
)

type feedEntry struct {
	ID    string `json:"id"`
	URL   string `json:"url"`
	Title string `json:"title"`
}

// setFeed sets what the fake yt-dlp lists for any channel (newest first).
func (e *env) setFeed(title string, ids ...string) {
	e.t.Helper()
	var entries []feedEntry
	for _, id := range ids {
		entries = append(entries, feedEntry{ID: id, URL: "https://1.1.1.1/watch?v=" + id, Title: "Video " + id})
	}
	data, _ := json.Marshal(map[string]any{"title": title, "entries": entries})
	if err := os.WriteFile(filepath.Join(e.binDir, "feed.json"), data, 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// pollNow makes every subscription due and runs one poller pass.
func (e *env) pollNow() {
	e.t.Helper()
	if _, err := e.pool.Exec(context.Background(), `update subscriptions set next_check_at = now()`); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.poller.CheckDue(context.Background()); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) jobURLs(userID int64) []string {
	jobs, _ := db.GetUserJobs(context.Background(), e.pool, userID, 50)
	var out []string
	for _, j := range jobs {
		out = append(out, j.URL)
	}
	return out
}

// Following a channel ignores what's already there and delivers new uploads.
func TestE2EFollowDeliversNewUploads(t *testing.T) {
	e := newEnv(t)
	const user = 701
	e.setFeed("Cool Channel", "e2", "e1")

	e.send(private(user), user, "/follow https://1.1.1.1/@cool")
	if !strings.Contains(e.api.LastText(user), "Following: Cool Channel") {
		t.Fatalf("follow reply = %q", e.api.LastText(user))
	}

	// Nothing new yet: no jobs.
	e.pollNow()
	if urls := e.jobURLs(user); len(urls) != 0 {
		t.Fatalf("existing uploads were queued: %v", urls)
	}

	e.setFeed("Cool Channel", "e3", "e2", "e1")
	e.pollNow()
	job := e.waitJob(user)
	if job.Status != db.StatusDone || job.URL != "https://1.1.1.1/watch?v=e3" {
		t.Fatalf("job = %+v", job)
	}
	if len(e.sendsTo("sendVideo", user)) != 1 {
		t.Fatal("new upload not delivered")
	}

	// The same entry is never delivered twice.
	e.pollNow()
	if urls := e.jobURLs(user); len(urls) != 1 {
		t.Fatalf("re-delivered: %v", urls)
	}
}

func TestE2EFollowCapsBurstsAndOrdersOldestFirst(t *testing.T) {
	e := newEnv(t)
	const user = 702
	e.setFeed("Busy", "a")
	e.send(private(user), user, "/follow https://1.1.1.1/@busy")
	e.setFeed("Busy", "h", "g", "f", "e", "d", "c", "b", "a")
	e.pollNow()
	jobs, _ := db.GetUserJobs(context.Background(), e.pool, user, 50)
	if len(jobs) != 5 {
		t.Fatalf("queued %d, want the 5 newest", len(jobs))
	}
	// GetUserJobs is newest first, so the last queued is the newest upload.
	if !strings.HasSuffix(jobs[0].URL, "=h") || !strings.HasSuffix(jobs[4].URL, "=d") {
		t.Fatalf("order = %v", e.jobURLs(user))
	}
	// Skipped older entries count as seen; nothing more next time.
	e.pollNow()
	if n := len(e.jobURLs(user)); n != 5 {
		t.Fatalf("jobs after second poll = %d", n)
	}
}

func TestE2EFollowingListAndUnfollow(t *testing.T) {
	e := newEnv(t)
	const user = 703
	e.setFeed("One", "x")
	e.send(private(user), user, "/follow https://1.1.1.1/@one")
	e.send(private(user), user, "/follow https://1.1.1.1/@one")
	if !strings.Contains(e.api.LastText(user), "already follow") {
		t.Fatalf("duplicate follow = %q", e.api.LastText(user))
	}
	e.send(private(user), user, "/follow https://1.1.1.1/@two audio")

	e.send(private(user), user, "/following")
	list := e.api.LastText(user)
	if !strings.Contains(list, "1. One") || !strings.Contains(list, "🎵") {
		t.Fatalf("list = %q", list)
	}
	subs, _ := db.ListSubscriptions(context.Background(), e.pool, user)
	if len(subs) != 2 || subs[1].Mode != db.ModeAudio {
		t.Fatalf("subs = %+v", subs)
	}

	// Another user can't remove it, even with the right ID.
	e.press(private(704), 704, fmt.Sprintf("unf:%d", subs[0].ID))
	if s, _ := db.ListSubscriptions(context.Background(), e.pool, user); len(s) != 2 {
		t.Fatal("another chat removed a subscription")
	}
	e.press(private(user), user, fmt.Sprintf("unf:%d", subs[0].ID))
	if s, _ := db.ListSubscriptions(context.Background(), e.pool, user); len(s) != 1 {
		t.Fatalf("unfollow button failed: %+v", s)
	}
	e.send(private(user), user, "/unfollow 1")
	if s, _ := db.ListSubscriptions(context.Background(), e.pool, user); len(s) != 0 || !strings.Contains(e.api.LastText(user), "Stopped following") {
		t.Fatalf("/unfollow failed: %q", e.api.LastText(user))
	}
}

func TestE2EFollowInGroupNeedsAdmin(t *testing.T) {
	e := newEnv(t)
	const g, admin, member = -3001, 705, 706
	e.setFeed("Group Feed", "x")
	e.api.SetChatAdmin(g, admin)
	e.send(supergroup(g), member, "/follow https://1.1.1.1/@gf")
	if s, _ := db.ListSubscriptions(context.Background(), e.pool, g); len(s) != 0 {
		t.Fatal("a regular member subscribed the group")
	}
	e.send(supergroup(g), admin, "/follow https://1.1.1.1/@gf")
	if s, _ := db.ListSubscriptions(context.Background(), e.pool, g); len(s) != 1 {
		t.Fatal("group admin could not subscribe")
	}
}

func TestE2EFollowBadFeed(t *testing.T) {
	e := newEnv(t)
	const user = 707
	// No feed.json: the fake yt-dlp fails like an unreadable channel.
	e.send(private(user), user, "/follow https://1.1.1.1/@missing")
	if !strings.Contains(e.api.LastText(user), "Couldn't read") {
		t.Fatalf("got %q", e.api.LastText(user))
	}
}

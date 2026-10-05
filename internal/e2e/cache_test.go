package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/media"
)

func (e *env) cacheEntry(prefix, url string) *db.CacheEntry {
	e.t.Helper()
	mode, quality := "video", ""
	switch {
	case prefix == "audio:":
		mode = "audio"
	case prefix != "":
		quality = strings.TrimSuffix(prefix, ":")
	}
	entry, ok, err := db.GetCache(context.Background(), e.pool, media.CacheKey(url, mode, quality, ""))
	if err != nil || !ok {
		e.t.Fatalf("no cache entry for %s%s (err=%v)", prefix, url, err)
	}
	return entry
}

func (e *env) lastJob(userID int64) db.Job {
	e.t.Helper()
	jobs, err := db.GetUserJobs(context.Background(), e.pool, userID, 1)
	if err != nil || len(jobs) == 0 {
		e.t.Fatalf("no jobs for %d: %v", userID, err)
	}
	return jobs[0]
}

func (e *env) sendsTo(method string, chatID int64) []string {
	var ids []string
	field := strings.ToLower(strings.TrimPrefix(method, "send"))
	for _, c := range e.api.CallsTo(method) {
		if c.Fields["chat_id"] == fmt.Sprint(chatID) {
			if c.Fields[field] != "" {
				ids = append(ids, c.Fields[field]) // re-sent by file ID
			} else {
				ids = append(ids, "upload:"+c.Files[field].Name)
			}
		}
	}
	return ids
}

// The second request for the same content, even via a different link form,
// is served from Telegram's copy without downloading again, and does not
// use up the daily quota.
func TestE2ECacheServesRepeatInstantly(t *testing.T) {
	e := newEnv(t, func(c *config.Root) { c.Downly.Limits.DailyQuotaPerUser = 1 })
	const alice, bob = 301, 302

	e.send(private(alice), alice, videoURL)
	if j := e.waitJob(alice); j.Status != db.StatusDone {
		t.Fatalf("first job = %+v", j)
	}
	if e.downloads() != 1 {
		t.Fatalf("downloads = %d", e.downloads())
	}
	fileID := e.cacheEntry("", videoURL).Items[0].FileID

	e.send(private(bob), bob, "https://1.1.1.1/watch?v=vid42&si=SHARE")
	if got := e.sendsTo("sendVideo", bob); len(got) != 1 || got[0] != fileID {
		t.Fatalf("bob got %v, want the cached %s", got, fileID)
	}
	if e.downloads() != 1 {
		t.Fatal("cache hit must not download again")
	}
	if j := e.lastJob(bob); !j.Cached || j.Status != db.StatusDone {
		t.Fatalf("bob's job = %+v", j)
	}

	// Alice already used her daily quota of 1; cached deliveries are free.
	e.send(private(alice), alice, videoURL)
	if got := e.sendsTo("sendVideo", alice); len(got) != 2 || got[1] != fileID {
		t.Fatalf("alice's repeat = %v", got)
	}
	if e.api.AnyText(alice, "Daily limit") {
		t.Fatal("cached delivery was blocked by the daily quota")
	}
}

func TestE2EHistorySendAgain(t *testing.T) {
	e := newEnv(t)
	const user, other = 311, 312
	e.send(private(user), user, videoURL)
	job := e.waitJob(user)

	e.send(private(user), user, "/history")
	if !strings.Contains(e.api.LastMarkup(user), fmt.Sprintf(`"again:%d"`, job.ID)) {
		t.Fatalf("history has no send-again button: %s", e.api.LastMarkup(user))
	}
	e.press(private(user), user, fmt.Sprintf("again:%d", job.ID))
	if got := e.sendsTo("sendVideo", user); len(got) != 2 || strings.HasPrefix(got[1], "upload:") {
		t.Fatalf("resend should reuse the file ID, got %v", got)
	}
	if e.downloads() != 1 {
		t.Fatal("resend downloaded again")
	}

	// Someone else's job ID is refused.
	e.press(private(other), other, fmt.Sprintf("again:%d", job.ID))
	if len(e.sendsTo("sendVideo", other)) != 0 {
		t.Fatal("another user resent someone else's download")
	}
}

// If Telegram no longer accepts a cached file ID, the entry is dropped and
// the content is downloaded again transparently.
func TestE2EStaleCacheFallsBackToDownload(t *testing.T) {
	e := newEnv(t)
	const a, b = 321, 322
	e.send(private(a), a, videoURL)
	e.waitJob(a)
	stale := e.cacheEntry("", videoURL).Items[0].FileID
	e.api.RejectFileID(stale)

	e.send(private(b), b, videoURL)
	if j := e.waitJob(b); j.Status != db.StatusDone || j.Cached {
		t.Fatalf("job = %+v", j)
	}
	if e.downloads() != 2 {
		t.Fatalf("downloads = %d, want a fresh download", e.downloads())
	}
	if fresh := e.cacheEntry("", videoURL).Items[0].FileID; fresh == stale {
		t.Fatal("stale file ID still cached")
	}
}

func (e *env) inline(userID int64, query string) string {
	updateID++
	e.b.ProcessUpdate(context.Background(), &models.Update{
		ID:          updateID,
		InlineQuery: &models.InlineQuery{ID: fmt.Sprint(updateID), From: &models.User{ID: userID}, Query: query},
	})
	calls := e.api.CallsTo("answerInlineQuery")
	if len(calls) == 0 {
		e.t.Fatal("inline query not answered")
	}
	return calls[len(calls)-1].Fields["results"]
}

func (e *env) choose(userID int64, resultID, query, inlineID string) {
	updateID++
	e.b.ProcessUpdate(context.Background(), &models.Update{
		ID: updateID,
		ChosenInlineResult: &models.ChosenInlineResult{
			ResultID: resultID, From: models.User{ID: userID}, Query: query, InlineMessageID: inlineID,
		},
	})
}

// Inline mode: a fresh download replaces the shared placeholder with the
// video; afterwards the same link is offered straight from the cache.
func TestE2EInlineMode(t *testing.T) {
	e := newEnv(t)
	const user = 331

	results := e.inline(user, videoURL)
	if !strings.Contains(results, `"dl_720"`) || !strings.Contains(results, `"noop"`) || strings.Contains(results, `"c_`) {
		t.Fatalf("uncached inline results = %s", results)
	}

	e.choose(user, "dl_720", videoURL, "inline-msg-1")
	job := e.waitJob(user)
	if job.Status != db.StatusDone || job.Quality != "q720" || job.InlineMessageID != "inline-msg-1" {
		t.Fatalf("job = %+v", job)
	}
	fileID := e.cacheEntry("q720:", videoURL).Items[0].FileID
	var edited bool
	for _, c := range e.api.CallsTo("editMessageMedia") {
		if c.Fields["inline_message_id"] == "inline-msg-1" && strings.Contains(c.Fields["media"], fileID) {
			edited = true
		}
	}
	if !edited {
		t.Fatal("inline placeholder was not replaced with the video")
	}

	results = e.inline(user, videoURL)
	if !strings.Contains(results, `"c_720"`) || !strings.Contains(results, `"video_file_id":"`+fileID+`"`) {
		t.Fatalf("cached variant not offered: %s", results)
	}
	if !strings.Contains(results, `"dl_best"`) {
		t.Fatal("uncached variants should still be offered")
	}

	before := e.downloads()
	e.choose(user, "c_720", videoURL, "")
	if e.downloads() != before {
		t.Fatal("choosing a cached result must not download")
	}
	if j := e.lastJob(user); !j.Cached {
		t.Fatalf("inline share not recorded: %+v", j)
	}
}

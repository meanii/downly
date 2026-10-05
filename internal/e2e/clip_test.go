package e2e

import (
	"context"
	"strings"
	"testing"

	"github.com/meanii/downly/internal/db"
)

// "link start-end" downloads only that section, cached separately from the
// full video.
func TestE2EClipFromMessage(t *testing.T) {
	e := newEnv(t)
	const user = 601
	e.send(private(user), user, videoURL+" 0:10-0:20")
	job := e.waitJob(user)
	if job.Status != db.StatusDone || job.ClipStart != 10 || job.ClipEnd != 20 {
		t.Fatalf("job = %+v", job)
	}
	calls := e.ytdlpCalls()
	if len(calls) != 1 || !strings.Contains(calls[0], "--download-sections *10-20") || !strings.Contains(calls[0], "--force-keyframes-at-cuts") {
		t.Fatalf("yt-dlp calls = %v", calls)
	}
	up := e.upload("sendVideo", user)
	if !strings.Contains(up.Fields["caption"], "(0:10-0:20)") || up.Fields["duration"] != "10" {
		t.Fatalf("caption=%q duration=%q", up.Fields["caption"], up.Fields["duration"])
	}

	// The full video is a different cache entry: it must be downloaded.
	e.send(private(user), user, videoURL)
	if j := e.waitJob(user); j.Cached {
		t.Fatal("full video served from the clip's cache entry")
	}
	if e.downloads() != 2 {
		t.Fatalf("downloads = %d", e.downloads())
	}
}

func TestE2EGIF(t *testing.T) {
	e := newEnv(t)
	const user = 602
	e.send(private(user), user, "/gif "+videoURL+" 0:02-0:05")
	job := e.waitJob(user)
	if job.Status != db.StatusDone || job.Mode != db.ModeGIF {
		t.Fatalf("job = %+v", job)
	}
	if calls := e.ytdlpCalls(); len(calls) != 1 || !strings.Contains(calls[0], "*2-5") {
		t.Fatalf("yt-dlp calls = %v", calls)
	}
	anim := e.upload("sendAnimation", user)
	if a := anim.Files["animation"]; string(a.Data) != "FAKE-MEDIA-mp4" || !strings.HasSuffix(a.Name, ".mp4") {
		t.Fatalf("animation upload = %+v", a)
	}

	// A repeat is served from the cache as an animation.
	e.send(private(user), user, "/gif "+videoURL+" 0:02-0:05")
	if got := e.sendsTo("sendAnimation", user); len(got) != 2 || strings.HasPrefix(got[1], "upload:") {
		t.Fatalf("cached GIF resend = %v", got)
	}
}

func TestE2EClipLimitsAndUsage(t *testing.T) {
	e := newEnv(t)
	const user = 603
	e.send(private(user), user, "/gif "+videoURL+" 0:00-0:45")
	if !strings.Contains(e.api.LastText(user), "at most 30 seconds") {
		t.Fatalf("got %q", e.api.LastText(user))
	}
	e.send(private(user), user, "/clip "+videoURL)
	if !strings.Contains(e.api.LastText(user), "Invalid time range") {
		t.Fatalf("got %q", e.api.LastText(user))
	}
	e.send(private(user), user, videoURL+" 0:00-30:00")
	if !strings.Contains(e.api.LastText(user), "at most 20 minutes") {
		t.Fatalf("got %q", e.api.LastText(user))
	}
	if jobs, _ := db.GetUserJobs(context.Background(), e.pool, user, 5); len(jobs) != 0 {
		t.Fatalf("refused requests created jobs: %+v", jobs)
	}
}

func TestE2EAudioIsTagged(t *testing.T) {
	e := newEnv(t)
	const user = 604
	e.send(private(user), user, "/mp3 "+videoURL)
	e.waitJob(user)
	calls := e.ytdlpCalls()
	if len(calls) != 1 || !strings.Contains(calls[0], "--embed-metadata") || !strings.Contains(calls[0], "--embed-thumbnail") {
		t.Fatalf("yt-dlp calls = %v", calls)
	}
}

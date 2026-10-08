package e2e

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meanii/downly/internal/config"
	"github.com/meanii/downly/internal/db"
)

// Regression: with the cookies file mounted read-only (as compose.yaml
// does), yt-dlp crashed writing it back, and every Instagram download failed
// as "no video or image could be extracted".
func TestE2EReadOnlyCookiesStillDownload(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	dir := t.TempDir()
	cookies := filepath.Join(dir, "instagram.txt")
	const original = "# Netscape HTTP Cookie File\n"
	if err := os.WriteFile(cookies, []byte(original), 0o444); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, func(c *config.Root) { c.Downly.Services.YTDLP.CookiesFile = cookies })
	const user = 901
	e.send(private(user), user, videoURL)
	job := e.waitJob(user)
	if job.Status != db.StatusDone {
		t.Fatalf("job = %s: %s", job.Status, job.ErrorMessage)
	}
	if len(e.sendsTo("sendVideo", user)) != 1 {
		t.Fatal("video not delivered")
	}
	if data, _ := os.ReadFile(cookies); string(data) != original {
		t.Fatalf("original cookies file was modified: %q", data)
	}
}

// A yt-dlp crash must surface as a retryable internal error (not be masked
// by the image fallback as "no video or image"), and count in /health.
func TestE2ECrashIsReportedAndCounted(t *testing.T) {
	e := newEnv(t, func(c *config.Root) {
		c.Downly.Limits.MaxRetries = 0
		c.Downly.Admin.UserIDs = []int64{903}
	})
	const user = 902
	e.send(private(user), user, "https://www.instagram.com/reel/crash/")
	job := e.waitJob(user)
	if job.Status != db.StatusFailed {
		t.Fatalf("job = %+v", job)
	}
	if strings.Contains(job.ErrorMessage, "no video or image") || !strings.Contains(job.ErrorMessage, "Traceback") {
		t.Fatalf("crash masked: %q", job.ErrorMessage)
	}
	final := e.api.LastText(user)
	if !strings.Contains(final, "internal error") || strings.Contains(final, "Traceback") {
		t.Fatalf("user message = %q", final)
	}
	var platform string
	_ = e.pool.QueryRow(context.Background(), `select platform from download_jobs where id = $1`, job.ID).Scan(&platform)
	if platform != "instagram" {
		t.Fatalf("platform = %q, want instagram", platform)
	}

	e.send(private(903), 903, "/health")
	if h := e.api.LastText(903); !strings.Contains(h, "instagram [FAILING] — 0/1 ok") {
		t.Fatalf("health = %q", h)
	}
}

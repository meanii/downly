package worker

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/go-telegram/bot"

	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/safeurl"
)

func TestIsPermanent(t *testing.T) {
	permanent := []error{
		downloader.ErrTooLarge,
		fmt.Errorf("wrapped: %w", safeurl.ErrBlockedHost),
		errors.New("yt-dlp failed: ERROR: [youtube] abc: Private video. Sign in if you've been granted access"),
		errors.New("yt-dlp failed: ERROR: Unsupported URL: https://example.com"),
		errors.New("yt-dlp failed: ERROR: [youtube] x: Video unavailable"),
		errors.New("File is larger than max-filesize (300000000 bytes > 209715200 bytes). Aborting."),
	}
	transient := []error{
		errors.New("yt-dlp failed: ERROR: unable to download video data: HTTP Error 503: Service Unavailable"),
		errors.New("yt-dlp failed: ERROR: Connection reset by peer"),
		errors.New("timed out after 30m0s"),
	}
	for _, e := range permanent {
		if !isPermanent(e) {
			t.Errorf("should be permanent: %v", e)
		}
	}
	for _, e := range transient {
		if isPermanent(e) {
			t.Errorf("should be retryable: %v", e)
		}
	}
	if isPermanent(nil) {
		t.Error("nil is not permanent")
	}
}

func TestIsPermanentUploadErr(t *testing.T) {
	if !isPermanentUploadErr(fmt.Errorf("%w, chat not found", bot.ErrorBadRequest)) {
		t.Error("bad request should be permanent")
	}
	if !isPermanentUploadErr(fmt.Errorf("%w, bot was blocked by the user", bot.ErrorForbidden)) {
		t.Error("forbidden should be permanent")
	}
	if isPermanentUploadErr(errors.New("connection reset")) {
		t.Error("network error should be retryable")
	}
}

func TestRetryDelay(t *testing.T) {
	want := []time.Duration{30 * time.Second, 2 * time.Minute, 8 * time.Minute, 30 * time.Minute, 30 * time.Minute}
	for i, w := range want {
		if got := retryDelay(i); got != w {
			t.Errorf("retryDelay(%d) = %v, want %v", i, got, w)
		}
	}
}

func TestFriendlyError(t *testing.T) {
	err := errors.New("yt-dlp failed: [youtube] Extracting URL\n[youtube] abc: Downloading webpage\nERROR: [youtube] abc: Video unavailable\nsome trailing noise")
	if got := friendlyError(err); got != "[youtube] abc: Video unavailable" {
		t.Fatalf("got %q", got)
	}
	if got := friendlyError(errors.New("plain")); got != "plain" {
		t.Fatalf("got %q", got)
	}
}

func TestTruncateRunesKeepsUTF8Valid(t *testing.T) {
	title := strings.Repeat("नमस्ते दुनिया ", 20) + "🎬"
	for _, n := range []int{5, 80, 100, 300} {
		got := truncateRunes(title, n)
		if !utf8.ValidString(got) {
			t.Fatalf("truncateRunes(%d) produced invalid UTF-8", n)
		}
		if utf8.RuneCountInString(got) > n {
			t.Fatalf("truncateRunes(%d) has %d runes", n, utf8.RuneCountInString(got))
		}
	}
	if truncateRunes("short", 10) != "short" {
		t.Fatal("short strings must be unchanged")
	}
}

func TestBuildCaptionUTF8(t *testing.T) {
	res := &downloader.Result{Title: strings.Repeat("é", 150)}
	if c := buildCaption(res); !utf8.ValidString(c) {
		t.Fatal("caption is not valid UTF-8")
	}
}

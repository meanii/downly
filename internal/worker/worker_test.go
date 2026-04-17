package worker

import (
	"testing"

	"github.com/meanii/downly/internal/downloader"
)

func TestProgressBar(t *testing.T) {
	tests := []struct {
		percent  int
		expected string
	}{
		{0, "[--------------------]"},
		{50, "[##########----------]"},
		{100, "[####################]"},
		{25, "[#####---------------]"},
		{-10, "[--------------------]"},
		{150, "[####################]"},
	}
	for _, tt := range tests {
		got := progressBar(tt.percent)
		if got != tt.expected {
			t.Errorf("progressBar(%d) = %q, want %q", tt.percent, got, tt.expected)
		}
	}
}

func TestBuildCaption(t *testing.T) {
	res := &downloader.Result{
		Title:    "Test Video Title",
		Platform: "youtube",
		Duration: 125,
	}
	caption := buildCaption(res)
	if caption == "" {
		t.Error("expected non-empty caption")
	}
	if !contains(caption, "Test Video Title") {
		t.Error("expected caption to contain title")
	}
	if !contains(caption, "2:05") {
		t.Error("expected caption to contain duration 2:05")
	}
	if !contains(caption, "youtube") {
		t.Error("expected caption to contain platform")
	}
}

func TestBuildCaptionEmpty(t *testing.T) {
	res := &downloader.Result{}
	caption := buildCaption(res)
	if caption != "Done." {
		t.Errorf("expected 'Done.' for empty result, got %q", caption)
	}
}

func TestBuildCaptionUnknownPlatform(t *testing.T) {
	res := &downloader.Result{
		Title:    "Some Title",
		Platform: "unknown",
	}
	caption := buildCaption(res)
	if contains(caption, "unknown") {
		t.Error("caption should not contain 'unknown' platform")
	}
}

func TestBuildCaptionLongTitle(t *testing.T) {
	longTitle := ""
	for i := 0; i < 200; i++ {
		longTitle += "a"
	}
	res := &downloader.Result{Title: longTitle, Platform: "test"}
	caption := buildCaption(res)
	if len(caption) > 200 {
		t.Errorf("caption too long: %d chars", len(caption))
	}
}

func TestTruncate(t *testing.T) {
	short := "hello"
	if truncate(short) != short {
		t.Error("truncate should not change short strings")
	}

	long := ""
	for i := 0; i < 500; i++ {
		long += "x"
	}
	result := truncate(long)
	if len(result) != 300 {
		t.Errorf("expected truncated to 300, got %d", len(result))
	}
}

func TestFormatDoneMessage(t *testing.T) {
	res := &downloader.Result{
		Platform: "instagram",
		Title:    "Cool Reel",
	}
	msg := formatDoneMessage(42, res)
	if !contains(msg, "#42") {
		t.Error("expected job ID in message")
	}
	if !contains(msg, "done") {
		t.Error("expected 'done' in message")
	}
	if !contains(msg, "instagram") {
		t.Error("expected platform in message")
	}
}

func TestParseJobURL(t *testing.T) {
	tests := []struct {
		raw         string
		wantURL     string
		wantMode    string
		wantQuality string
	}{
		{"https://youtube.com/watch?v=abc", "https://youtube.com/watch?v=abc", "default", ""},
		{"audio:https://youtube.com/watch?v=abc", "https://youtube.com/watch?v=abc", "audio", ""},
		{"q720:https://youtube.com/watch?v=abc", "https://youtube.com/watch?v=abc", "quality", "q720"},
		{"q480:https://youtube.com/watch?v=abc", "https://youtube.com/watch?v=abc", "quality", "q480"},
		{"q1080:https://youtube.com/watch?v=abc", "https://youtube.com/watch?v=abc", "quality", "q1080"},
		{"q360:https://youtube.com/watch?v=abc", "https://youtube.com/watch?v=abc", "quality", "q360"},
	}
	for _, tt := range tests {
		url, mode, quality := parseJobURL(tt.raw)
		if url != tt.wantURL {
			t.Errorf("parseJobURL(%q) url = %q, want %q", tt.raw, url, tt.wantURL)
		}
		if mode != tt.wantMode {
			t.Errorf("parseJobURL(%q) mode = %q, want %q", tt.raw, mode, tt.wantMode)
		}
		if quality != tt.wantQuality {
			t.Errorf("parseJobURL(%q) quality = %q, want %q", tt.raw, quality, tt.wantQuality)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStr(s, substr))
}

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

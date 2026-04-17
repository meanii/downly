package telegram

import (
	"testing"
)

func TestLooksLikeURL(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"https://youtube.com/watch?v=abc", true},
		{"http://example.com", true},
		{"www.youtube.com/watch?v=abc", true},
		{"youtube.com/watch?v=abc", false},
		{"not a url", false},
		{"/start", false},
		{"", false},
		{"  https://test.com  ", true},
		{"HTTP://TEST.COM", true},
		{"WWW.TEST.COM", true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := looksLikeURL(tt.input)
			if got != tt.expected {
				t.Errorf("looksLikeURL(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"www.youtube.com", "https://www.youtube.com"},
		{"WWW.YouTube.com", "https://WWW.YouTube.com"},
		{"https://youtube.com", "https://youtube.com"},
		{"http://youtube.com", "http://youtube.com"},
		{"  www.test.com  ", "https://www.test.com"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := normalizeURL(tt.input)
			if got != tt.expected {
				t.Errorf("normalizeURL(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestExtractURLs(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"https://youtube.com/a https://youtube.com/b", 2},
		{"https://youtube.com/a some text https://test.com/b", 2},
		{"no urls here", 0},
		{"www.test.com", 1},
		{"single https://test.com", 1},
		{"https://a.com https://b.com https://c.com", 3},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			urls := extractURLs(tt.input)
			if len(urls) != tt.expected {
				t.Errorf("extractURLs(%q) returned %d URLs, want %d", tt.input, len(urls), tt.expected)
			}
		})
	}
}

func TestContainsURL(t *testing.T) {
	if !containsURL("check https://test.com out") {
		t.Error("expected true for text with URL")
	}
	if !containsURL("www.test.com is good") {
		t.Error("expected true for text with www URL")
	}
	if containsURL("no urls here at all") {
		t.Error("expected false for text without URL")
	}
	if !containsURL("q720:https://test.com") {
		t.Error("expected true for quality-prefixed URL")
	}
	if !containsURL("audio:https://test.com") {
		t.Error("expected true for audio-prefixed URL")
	}
}

func TestExtractURLsWithPrefix(t *testing.T) {
	urls := extractURLs("q720:https://youtube.com/watch?v=abc")
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL, got %d", len(urls))
	}
	if urls[0] != "q720:https://youtube.com/watch?v=abc" {
		t.Errorf("expected prefixed URL preserved, got %q", urls[0])
	}

	urls = extractURLs("audio:https://test.com")
	if len(urls) != 1 {
		t.Fatalf("expected 1 URL, got %d", len(urls))
	}
	if urls[0] != "audio:https://test.com" {
		t.Errorf("expected audio prefix preserved, got %q", urls[0])
	}
}

func TestStripModePrefix(t *testing.T) {
	tests := []struct {
		input      string
		wantClean  string
		wantPrefix string
	}{
		{"https://test.com", "https://test.com", ""},
		{"q720:https://test.com", "https://test.com", "q720:"},
		{"audio:https://test.com", "https://test.com", "audio:"},
		{"q1080:https://test.com", "https://test.com", "q1080:"},
	}
	for _, tt := range tests {
		clean, prefix := stripModePrefix(tt.input)
		if clean != tt.wantClean {
			t.Errorf("stripModePrefix(%q) clean = %q, want %q", tt.input, clean, tt.wantClean)
		}
		if prefix != tt.wantPrefix {
			t.Errorf("stripModePrefix(%q) prefix = %q, want %q", tt.input, prefix, tt.wantPrefix)
		}
	}
}

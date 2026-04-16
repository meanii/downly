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
}

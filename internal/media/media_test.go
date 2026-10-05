package media

import (
	"testing"

	"github.com/go-telegram/bot/models"
)

func TestCanonicalURL(t *testing.T) {
	same := [][]string{
		{"https://www.youtube.com/watch?v=abc", "https://youtu.be/abc?si=XYZ", "http://m.youtube.com/watch?v=abc&feature=share", "https://youtube.com/watch?v=abc#t=5"},
		{"https://www.instagram.com/reel/C1/?igsh=abc", "https://instagram.com/reel/C1", "https://instagram.com/reel/C1/?utm_source=ig_web"},
		{"https://x.com/a/status/1?b=2&a=1", "https://x.com/a/status/1?a=1&b=2"},
	}
	for _, group := range same {
		want := CanonicalURL(group[0])
		for _, u := range group[1:] {
			if got := CanonicalURL(u); got != want {
				t.Errorf("CanonicalURL(%q) = %q, want %q", u, got, want)
			}
		}
	}
	// Different content must stay different.
	if CanonicalURL("https://youtube.com/watch?v=a") == CanonicalURL("https://youtube.com/watch?v=b") {
		t.Error("different videos collapsed")
	}
	if CanonicalURL("https://example.com/v?id=1") == CanonicalURL("https://example.com/v?id=2") {
		t.Error("meaningful query parameters must be kept")
	}
	if got := CanonicalURL("not a url"); got != "not a url" {
		t.Errorf("unparseable input changed: %q", got)
	}
}

func TestCacheKey(t *testing.T) {
	a := CacheKey("https://youtu.be/abc", "video", "q720", "")
	if a != CacheKey("https://www.youtube.com/watch?v=abc&si=1", "video", "q720", "") {
		t.Error("equivalent URLs should share a key")
	}
	for _, other := range []string{
		CacheKey("https://youtu.be/abc", "audio", "", ""),
		CacheKey("https://youtu.be/abc", "video", "q480", ""),
		CacheKey("https://youtu.be/abc", "video", "q720", "clip:10-20"),
	} {
		if other == a {
			t.Errorf("key collision: %s", other)
		}
	}
}

func TestItemFromMessage(t *testing.T) {
	cases := []struct {
		m    *models.Message
		want Item
	}{
		{&models.Message{Video: &models.Video{FileID: "v"}}, Item{Video, "v"}},
		{&models.Message{Audio: &models.Audio{FileID: "a"}}, Item{Audio, "a"}},
		{&models.Message{Photo: []models.PhotoSize{{FileID: "small"}, {FileID: "big"}}}, Item{Photo, "big"}},
		{&models.Message{Document: &models.Document{FileID: "d"}}, Item{Document, "d"}},
		// Telegram sets both animation and document for GIFs; animation wins.
		{&models.Message{Animation: &models.Animation{FileID: "g"}, Document: &models.Document{FileID: "g"}}, Item{Animation, "g"}},
	}
	for _, c := range cases {
		got, ok := ItemFromMessage(c.m)
		if !ok || got != c.want {
			t.Errorf("got %+v %v, want %+v", got, ok, c.want)
		}
	}
	if _, ok := ItemFromMessage(&models.Message{Text: "hi"}); ok {
		t.Error("text message has no media")
	}
}

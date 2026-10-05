package telegram

import (
	"strings"
	"testing"
	"time"
)

func TestURLStoreRoundTrip(t *testing.T) {
	s := newURLStore()
	long := "https://www.instagram.com/reel/ABCDEFGHIJK/?igsh=" + strings.Repeat("x", 200)
	tok := s.Put(long)
	if got, ok := s.Get(tok); !ok || got != long {
		t.Fatalf("Get = %q, %v", got, ok)
	}
	if _, ok := s.Get("missing"); ok {
		t.Fatal("unknown token should miss")
	}
}

func TestURLStoreExpiry(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newURLStore()
	s.now = func() time.Time { return now }
	tok := s.Put("https://a.com")
	now = now.Add(urlTokenTTL + time.Second)
	if _, ok := s.Get(tok); ok {
		t.Fatal("expired token should miss")
	}
}

func TestURLStoreBounded(t *testing.T) {
	s := newURLStore()
	for i := 0; i < urlStoreMaxSize+50; i++ {
		s.Put("https://a.com")
	}
	if len(s.entries) > urlStoreMaxSize {
		t.Fatalf("store grew to %d", len(s.entries))
	}
}

func TestQualityCallbackDataFitsTelegramLimit(t *testing.T) {
	tok := newURLStore().Put("https://example.com/" + strings.Repeat("a", 2000))
	for _, q := range qualityOptions {
		data := qualityCallbackData(q.Callback, tok)
		if len(data) > 64 {
			t.Errorf("callback data %q is %d bytes, Telegram allows 64", data, len(data))
		}
	}
}

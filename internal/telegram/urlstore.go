package telegram

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// Telegram limits callback_data to 64 bytes, far less than many media URLs.
// Buttons therefore carry a short token and the URL is kept here.
const (
	urlTokenTTL     = 24 * time.Hour
	urlStoreMaxSize = 10000
)

type urlEntry struct {
	url     string
	expires time.Time
}

type urlStore struct {
	mu      sync.Mutex
	entries map[string]urlEntry
	now     func() time.Time
}

func newURLStore() *urlStore {
	return &urlStore{entries: make(map[string]urlEntry), now: time.Now}
}

// Put stores url and returns a 12-character token for it.
func (s *urlStore) Put(url string) string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	token := base64.RawURLEncoding.EncodeToString(b[:])

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if len(s.entries) >= urlStoreMaxSize {
		s.evictLocked(now)
	}
	s.entries[token] = urlEntry{url: url, expires: now.Add(urlTokenTTL)}
	return token
}

// Get returns the URL for token, if it exists and has not expired.
func (s *urlStore) Get(token string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[token]
	if !ok {
		return "", false
	}
	if s.now().After(e.expires) {
		delete(s.entries, token)
		return "", false
	}
	return e.url, true
}

// evictLocked drops expired entries, and if that is not enough, the entries
// closest to expiry, so memory stays bounded.
func (s *urlStore) evictLocked(now time.Time) {
	for k, e := range s.entries {
		if now.After(e.expires) {
			delete(s.entries, k)
		}
	}
	for len(s.entries) >= urlStoreMaxSize {
		var oldestKey string
		var oldest time.Time
		for k, e := range s.entries {
			if oldestKey == "" || e.expires.Before(oldest) {
				oldestKey, oldest = k, e.expires
			}
		}
		delete(s.entries, oldestKey)
	}
}

var pendingURLs = newURLStore()

// Package tgutil holds small helpers for talking to the Telegram Bot API
// politely: rate-limit aware retries and edit throttling.
package tgutil

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/go-telegram/bot"
)

// MaxRetryAfter caps how long we are willing to wait on a single 429.
const MaxRetryAfter = 60 * time.Second

// OnRateLimited, if set, is called whenever Telegram answers 429. It is
// meant for metrics and must be set before the bot starts.
var OnRateLimited func()

func noteRateLimited(wait time.Duration) {
	if wait > 0 && OnRateLimited != nil {
		OnRateLimited()
	}
}

// RetryAfter returns the wait Telegram asked for, or 0 if err is not a 429.
func RetryAfter(err error) time.Duration {
	var tm *bot.TooManyRequestsError
	if errors.As(err, &tm) {
		d := time.Duration(tm.RetryAfter) * time.Second
		if d <= 0 {
			d = time.Second
		}
		if d > MaxRetryAfter {
			d = MaxRetryAfter
		}
		return d
	}
	return 0
}

// IsNotModified reports Telegram's harmless "message is not modified" error.
func IsNotModified(err error) bool {
	return err != nil && strings.Contains(err.Error(), "message is not modified")
}

// Call runs fn, retrying up to attempts times when Telegram answers 429.
func Call(ctx context.Context, attempts int, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var err error
	for i := 0; i < attempts; i++ {
		err = fn()
		wait := RetryAfter(err)
		noteRateLimited(wait)
		if wait == 0 || i == attempts-1 {
			return err
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
	return err
}

// Throttle decides whether a progress edit should be sent now. It enforces
// a minimum interval between edits, drops duplicates, and backs off after a 429.
type Throttle struct {
	Interval time.Duration
	Now      func() time.Time

	mu           sync.Mutex
	lastSent     time.Time
	lastText     string
	blockedUntil time.Time
}

func (t *Throttle) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Allow reports whether text may be sent now, and records it as sent if so.
// force bypasses the interval (but not a 429 back-off or duplicate check).
func (t *Throttle) Allow(text string, force bool) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if text == t.lastText {
		return false
	}
	if now.Before(t.blockedUntil) {
		return false
	}
	if !force && !t.lastSent.IsZero() && now.Sub(t.lastSent) < t.Interval {
		return false
	}
	t.lastSent = now
	t.lastText = text
	return true
}

// Observe feeds the result of a send back so 429s pause further edits and
// failed sends are not treated as delivered.
func (t *Throttle) Observe(err error) {
	if err == nil || IsNotModified(err) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastText = ""
	if wait := RetryAfter(err); wait > 0 {
		noteRateLimited(wait)
		t.blockedUntil = t.now().Add(wait)
	}
}

package tgutil

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-telegram/bot"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func TestThrottleInterval(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	th := &Throttle{Interval: 3 * time.Second, Now: c.now}

	if !th.Allow("a", false) {
		t.Fatal("first edit should pass")
	}
	if th.Allow("b", false) {
		t.Fatal("edit inside interval should be dropped")
	}
	c.advance(3 * time.Second)
	if !th.Allow("b", false) {
		t.Fatal("edit after interval should pass")
	}
	if th.Allow("b", true) {
		t.Fatal("duplicate text should be dropped even when forced")
	}
	if !th.Allow("c", true) {
		t.Fatal("forced edit should bypass interval")
	}
}

func TestThrottleBacksOffOn429(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	th := &Throttle{Interval: time.Second, Now: c.now}
	th.Allow("a", false)
	th.Observe(&bot.TooManyRequestsError{Message: "slow down", RetryAfter: 10})

	c.advance(5 * time.Second)
	if th.Allow("b", true) {
		t.Fatal("should be blocked during retry_after even when forced")
	}
	c.advance(6 * time.Second)
	if !th.Allow("b", false) {
		t.Fatal("should be allowed after retry_after")
	}
}

func TestThrottleFailedSendCanBeRetried(t *testing.T) {
	c := &fakeClock{t: time.Unix(1000, 0)}
	th := &Throttle{Interval: time.Second, Now: c.now}
	th.Allow("a", false)
	th.Observe(errors.New("network down"))
	c.advance(time.Second)
	if !th.Allow("a", false) {
		t.Fatal("same text should be resendable after a failed send")
	}
}

func TestThrottleNotModifiedIsSuccess(t *testing.T) {
	th := &Throttle{Interval: time.Second}
	th.Allow("a", false)
	th.Observe(errors.New("Bad Request: message is not modified"))
	if th.Allow("a", true) {
		t.Fatal("not-modified should count as delivered")
	}
}

func TestCallRetriesOn429(t *testing.T) {
	calls := 0
	err := Call(context.Background(), 3, func() error {
		calls++
		if calls < 2 {
			return &bot.TooManyRequestsError{Message: "x", RetryAfter: 0}
		}
		return nil
	})
	if err != nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestCallDoesNotRetryOtherErrors(t *testing.T) {
	calls := 0
	want := errors.New("bad request")
	err := Call(context.Background(), 3, func() error { calls++; return want })
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestCallStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Call(ctx, 3, func() error {
		return &bot.TooManyRequestsError{Message: "x", RetryAfter: 30}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

func TestRetryAfterCap(t *testing.T) {
	if d := RetryAfter(&bot.TooManyRequestsError{RetryAfter: 3600}); d != MaxRetryAfter {
		t.Fatalf("got %v", d)
	}
	if d := RetryAfter(errors.New("x")); d != 0 {
		t.Fatalf("got %v", d)
	}
}

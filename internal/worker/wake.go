package worker

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Waker broadcasts "new work may be available" to all idle workers.
type Waker struct {
	mu sync.Mutex
	ch chan struct{}
}

func NewWaker() *Waker { return &Waker{ch: make(chan struct{})} }

// C returns a channel that is closed on the next Broadcast. Grab it before
// checking for work so a notification in between is not missed.
func (w *Waker) C() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ch
}

// Broadcast wakes everyone currently waiting.
func (w *Waker) Broadcast() {
	w.mu.Lock()
	defer w.mu.Unlock()
	close(w.ch)
	w.ch = make(chan struct{})
}

// NotifyChannel is the Postgres channel the jobs trigger notifies on.
const NotifyChannel = "downly_jobs"

// Listen relays Postgres NOTIFYs on NotifyChannel to waker until ctx ends,
// reconnecting with backoff. Workers still poll, so a lost listener only
// costs latency, never correctness.
func Listen(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, waker *Waker) {
	log := logger.With("component", "worker", "listener", NotifyChannel)
	backoff := time.Second
	for ctx.Err() == nil {
		err := listenOnce(ctx, pool, waker)
		if ctx.Err() != nil {
			return
		}
		log.Warn("job listener disconnected, retrying", "error", err, "backoff", backoff)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func listenOnce(ctx context.Context, pool *pgxpool.Pool, waker *Waker) error {
	pc, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// LISTEN state lives on the connection; take it out of the pool so it is
	// never handed to anyone else, and close it when done.
	conn := pc.Hijack()
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "listen "+NotifyChannel); err != nil {
		return err
	}
	// Anything inserted while we were (re)connecting deserves a look.
	waker.Broadcast()
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		waker.Broadcast()
	}
}

package worker

import (
	"context"
	"sync"
	"time"

	"github.com/meanii/downly/internal/tgutil"
)

// progressInterval is the minimum gap between progress edits of one message.
// Telegram tolerates roughly one edit per second per chat; staying well below
// that leaves headroom for several concurrent jobs in the same chat.
const progressInterval = 3 * time.Second

// progressReporter turns a stream of downloader progress callbacks into a
// throttled trickle of DB writes and Telegram edits. It is safe for
// concurrent use.
type progressReporter struct {
	ctx      context.Context
	store    func(ctx context.Context, text string, percent int) error
	edit     func(ctx context.Context, text string) error
	format   func(text string, percent int) string
	throttle *tgutil.Throttle

	mu          sync.Mutex
	lastPercent int
}

func newProgressReporter(ctx context.Context, store func(context.Context, string, int) error, edit func(context.Context, string) error, format func(string, int) string) *progressReporter {
	return &progressReporter{
		ctx:         ctx,
		store:       store,
		edit:        edit,
		format:      format,
		throttle:    &tgutil.Throttle{Interval: progressInterval},
		lastPercent: -1,
	}
}

// Update records progress. Calls that arrive faster than progressInterval are
// dropped, except that the very first update is always shown.
func (p *progressReporter) Update(text string, percent int) {
	p.mu.Lock()
	if percent == p.lastPercent {
		p.mu.Unlock()
		return
	}
	p.lastPercent = percent
	p.mu.Unlock()

	msg := p.format(text, percent)
	if !p.throttle.Allow(msg, false) {
		return
	}
	ctx := p.context()
	_ = p.store(ctx, text, percent)
	p.throttle.Observe(p.edit(ctx, msg))
}

// SetContext switches the context used for later writes, e.g. from the
// (now finished) download context to the upload context.
func (p *progressReporter) SetContext(ctx context.Context) {
	p.mu.Lock()
	p.ctx = ctx
	p.mu.Unlock()
}

func (p *progressReporter) context() context.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ctx
}

// Force shows text immediately (subject only to a 429 back-off). Use it for
// stage changes the user should always see, e.g. "Uploading".
func (p *progressReporter) Force(text string, percent int) {
	msg := p.format(text, percent)
	ctx := p.context()
	_ = p.store(ctx, text, percent)
	if !p.throttle.Allow(msg, true) {
		return
	}
	p.throttle.Observe(p.edit(ctx, msg))
}

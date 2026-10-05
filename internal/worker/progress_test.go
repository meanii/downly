package worker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-telegram/bot"
)

func newTestReporter(edits, stores *int32, editErr func() error) *progressReporter {
	return newProgressReporter(context.Background(),
		func(context.Context, string, int) error { atomic.AddInt32(stores, 1); return nil },
		func(context.Context, string) error {
			atomic.AddInt32(edits, 1)
			if editErr != nil {
				return editErr()
			}
			return nil
		},
		func(text string, percent int) string { return fmt.Sprintf("%s %d", text, percent) },
	)
}

func TestProgressReporterThrottlesBursts(t *testing.T) {
	var edits, stores int32
	p := newTestReporter(&edits, &stores, nil)
	for i := 0; i <= 100; i++ {
		p.Update("Downloading", i)
	}
	if edits != 1 || stores != 1 {
		t.Fatalf("100 rapid updates should produce 1 edit/store, got edits=%d stores=%d", edits, stores)
	}
}

func TestProgressReporterForceBypassesInterval(t *testing.T) {
	var edits, stores int32
	p := newTestReporter(&edits, &stores, nil)
	p.Update("Downloading", 10)
	p.Force("Uploading to Telegram", 99)
	if edits != 2 {
		t.Fatalf("forced update should be sent, edits=%d", edits)
	}
}

func TestProgressReporterRespects429(t *testing.T) {
	var edits, stores int32
	p := newTestReporter(&edits, &stores, func() error {
		return &bot.TooManyRequestsError{Message: "slow", RetryAfter: 30}
	})
	p.Update("Downloading", 10)
	p.Force("Uploading", 99)
	if edits != 1 {
		t.Fatalf("no edits should be attempted during retry_after, edits=%d", edits)
	}
	if stores != 2 {
		t.Fatalf("DB progress should still be recorded on Force, stores=%d", stores)
	}
}

func TestProgressReporterAllowsUpdateAfterInterval(t *testing.T) {
	var edits, stores int32
	p := newTestReporter(&edits, &stores, nil)
	now := time.Unix(0, 0)
	p.throttle.Now = func() time.Time { return now }
	p.Update("Downloading", 10)
	now = now.Add(progressInterval)
	p.Update("Downloading", 20)
	if edits != 2 {
		t.Fatalf("edits=%d, want 2", edits)
	}
}

// Run with -race: stdout and stderr readers may report concurrently.
func TestProgressReporterConcurrentUse(t *testing.T) {
	var edits, stores int32
	p := newTestReporter(&edits, &stores, nil)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				p.Update("x", g*100+i)
			}
		}(g)
	}
	wg.Wait()
	p.SetContext(context.Background())
}

func TestUploadTimeout(t *testing.T) {
	if d := uploadTimeout(0); d != 2*time.Minute {
		t.Fatalf("got %v", d)
	}
	if d := uploadTimeout(50 * 1024 * 1024); d <= 2*time.Minute || d > 30*time.Minute {
		t.Fatalf("got %v", d)
	}
	if d := uploadTimeout(1 << 40); d != 30*time.Minute {
		t.Fatalf("got %v", d)
	}
}

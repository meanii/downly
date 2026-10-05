// Package subscriptions polls followed channels and playlists and queues
// their new uploads for download.
package subscriptions

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/meanii/downly/internal/db"
	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/media"
	"github.com/meanii/downly/internal/safeurl"
)

// Fetcher lists the newest entries of a channel or playlist.
type Fetcher interface {
	FetchPlaylist(ctx context.Context, url string, maxItems int) ([]downloader.PlaylistEntry, string, error)
}

const (
	// feedWindow is how many of the newest entries each check looks at.
	feedWindow = 10
	// maxNewPerCheck caps uploads queued per check, so a feed that suddenly
	// lists many "new" entries (e.g. after being reordered) can't flood a chat.
	maxNewPerCheck = 5
	// claimBatch is how many due subscriptions one tick handles.
	claimBatch = 10
	// lease keeps a claimed subscription from being picked up again while
	// it's being checked.
	lease = 10 * time.Minute
)

// Poller checks due subscriptions.
type Poller struct {
	Pool     *pgxpool.Pool
	Fetcher  Fetcher
	Interval time.Duration
	Log      *slog.Logger
}

// FeedURL normalizes a subscription URL: a YouTube channel link becomes its
// /videos tab, since the bare channel page lists tabs rather than uploads.
func FeedURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	host = strings.TrimPrefix(host, "m.")
	if host != "youtube.com" {
		return raw
	}
	path := strings.TrimSuffix(u.Path, "/")
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	isChannel := len(parts) >= 1 && (strings.HasPrefix(parts[0], "@") ||
		(len(parts) >= 2 && (parts[0] == "channel" || parts[0] == "c" || parts[0] == "user")))
	if !isChannel {
		return raw
	}
	base := 1
	if !strings.HasPrefix(parts[0], "@") {
		base = 2
	}
	if len(parts) == base {
		u.Path = path + "/videos"
		return u.String()
	}
	return raw
}

// Run checks due subscriptions every minute until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	log := p.Log.With("component", "subscriptions")
	log.Info("subscription poller started", "interval", p.Interval.String())
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if n, err := p.CheckDue(ctx); err != nil && ctx.Err() == nil {
			log.Error("check subscriptions failed", "error", err)
		} else if n > 0 {
			log.Info("checked subscriptions", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// CheckDue checks every subscription that is due and returns how many it
// checked.
func (p *Poller) CheckDue(ctx context.Context) (int, error) {
	subs, err := db.ClaimDueSubscriptions(ctx, p.Pool, claimBatch, lease)
	if err != nil {
		return 0, err
	}
	for _, s := range subs {
		p.check(ctx, s)
	}
	return len(subs), nil
}

func (p *Poller) check(ctx context.Context, s db.Subscription) {
	log := p.Log.With("component", "subscriptions", "subscription_id", s.ID, "chat_id", s.ChatID)
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	entries, title, err := p.Fetcher.FetchPlaylist(cctx, s.URL, feedWindow)
	if err != nil {
		next := p.backoff(s.Failures + 1)
		log.Warn("fetch feed failed", "error", err, "failures", s.Failures+1, "next_check", next.String())
		_ = db.RecordSubscriptionCheck(ctx, p.Pool, s.ID, false, "", next)
		return
	}
	ids := make([]string, len(entries))
	byID := make(map[string]downloader.PlaylistEntry, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
		byID[e.ID] = e
	}
	unseen, err := db.UnseenEntries(ctx, p.Pool, s.ID, ids)
	if err != nil {
		log.Error("load seen entries failed", "error", err)
		_ = db.RecordSubscriptionCheck(ctx, p.Pool, s.ID, false, "", p.backoff(s.Failures+1))
		return
	}
	// Feeds list newest first; deliver oldest first, at most maxNewPerCheck.
	if len(unseen) > maxNewPerCheck {
		// The skipped older ones are marked seen too: they're stale by now.
		unseen = unseen[:maxNewPerCheck]
	}
	for i := len(unseen) - 1; i >= 0; i-- {
		e := byID[unseen[i]]
		if _, err := safeurl.Validate(e.URL); err != nil {
			continue
		}
		job := db.NewJob{
			ChatID: s.ChatID, UserID: s.UserID, URL: e.URL, Mode: s.Mode,
			CacheKey: media.CacheKey(e.URL, string(s.Mode), "", ""),
		}
		if _, err := db.InsertJob(ctx, p.Pool, job); err != nil {
			log.Error("queue new upload failed", "entry", e.ID, "error", err)
			continue
		}
		log.Info("queued new upload", "entry", e.ID, "url", e.URL)
	}
	// Everything listed now is "seen", including entries beyond the cap.
	if err := db.MarkSeen(ctx, p.Pool, s.ID, ids); err != nil {
		log.Error("mark seen failed", "error", err)
	}
	_ = db.RecordSubscriptionCheck(ctx, p.Pool, s.ID, true, title, p.jittered())
}

// jittered spreads checks so subscriptions created together don't all hit
// the upstream site at once.
func (p *Poller) jittered() time.Duration {
	j := time.Duration(rand.Int64N(int64(p.Interval/10) + 1))
	return p.Interval + j
}

// backoff grows the delay after consecutive failures, up to a day.
func (p *Poller) backoff(failures int) time.Duration {
	d := p.Interval
	for i := 1; i < failures && d < 24*time.Hour; i++ {
		d *= 2
	}
	return min(d, 24*time.Hour)
}

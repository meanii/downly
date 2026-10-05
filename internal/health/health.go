// Package health tracks liveness of the bot's moving parts and exposes
// /health and Prometheus-style /metrics endpoints without extra dependencies.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Registry collects liveness signals and counters. The zero value is not
// usable; create one with New.
type Registry struct {
	now func() time.Time

	mu          sync.Mutex
	workers     map[string]time.Time
	telegramOK  time.Time
	telegramErr string
	started     time.Time

	counters sync.Map // name -> *atomic.Int64
}

func New() *Registry {
	return &Registry{now: time.Now, workers: map[string]time.Time{}, started: time.Now()}
}

// WorkerSeen records that a worker is alive (idle loop or job heartbeat).
func (r *Registry) WorkerSeen(id string) {
	r.mu.Lock()
	r.workers[id] = r.now()
	r.mu.Unlock()
}

// WorkerGone removes a worker that stopped on purpose.
func (r *Registry) WorkerGone(id string) {
	r.mu.Lock()
	delete(r.workers, id)
	r.mu.Unlock()
}

// TelegramResult records the outcome of a Telegram reachability probe.
func (r *Registry) TelegramResult(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.telegramOK = r.now()
		r.telegramErr = ""
		return
	}
	r.telegramErr = err.Error()
}

// Inc adds 1 to a counter. name may carry Prometheus labels, e.g.
// `downly_jobs_finished_total{result="done"}`.
func (r *Registry) Inc(name string) { r.Add(name, 1) }

func (r *Registry) Add(name string, n int64) {
	v, _ := r.counters.LoadOrStore(name, new(atomic.Int64))
	v.(*atomic.Int64).Add(n)
}

// Status is the /health payload.
type Status struct {
	OK               bool              `json:"ok"`
	Database         string            `json:"database"`
	Telegram         string            `json:"telegram"`
	TelegramLastOKAt *time.Time        `json:"telegram_last_ok_at,omitempty"`
	Workers          map[string]string `json:"workers"`
	UptimeSeconds    int64             `json:"uptime_seconds"`
}

// Thresholds for declaring a component unhealthy.
type Thresholds struct {
	// WorkerStale: a worker silent this long is considered stuck.
	WorkerStale time.Duration
	// TelegramStale: no successful probe for this long means Telegram is down.
	TelegramStale time.Duration
}

// Check evaluates current health. pingDB may be nil.
func (r *Registry) Check(ctx context.Context, pingDB func(context.Context) error, th Thresholds) Status {
	st := Status{OK: true, Database: "ok", Telegram: "ok", Workers: map[string]string{}}
	if pingDB != nil {
		if err := pingDB(ctx); err != nil {
			st.OK = false
			st.Database = err.Error()
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	st.UptimeSeconds = int64(now.Sub(r.started).Seconds())

	switch {
	case !r.telegramOK.IsZero():
		t := r.telegramOK
		st.TelegramLastOKAt = &t
		if now.Sub(r.telegramOK) > th.TelegramStale {
			st.OK = false
			st.Telegram = "unreachable since " + r.telegramOK.UTC().Format(time.RFC3339) + ": " + r.telegramErr
		}
	case now.Sub(r.started) > th.TelegramStale:
		st.OK = false
		st.Telegram = "never reached: " + r.telegramErr
	default:
		st.Telegram = "starting"
	}

	for id, seen := range r.workers {
		age := now.Sub(seen)
		if age > th.WorkerStale {
			st.OK = false
			st.Workers[id] = fmt.Sprintf("stale (%s)", age.Round(time.Second))
		} else {
			st.Workers[id] = "ok"
		}
	}
	return st
}

// ProbeTelegram calls probe every interval until ctx ends, recording results.
func (r *Registry) ProbeTelegram(ctx context.Context, log *slog.Logger, interval time.Duration, probe func(context.Context) error) {
	for {
		pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := probe(pctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Warn("telegram probe failed", "error", err)
		}
		r.TelegramResult(err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// Handler serves /health and /metrics.
func (r *Registry) Handler(pool *pgxpool.Pool, th Thresholds) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		var ping func(context.Context) error
		if pool != nil {
			ping = pool.Ping
		}
		st := r.Check(ctx, ping, th)
		w.Header().Set("Content-Type", "application/json")
		if !st.OK {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(st)
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, req *http.Request) {
		ctx, cancel := context.WithTimeout(req.Context(), 3*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		r.writeMetrics(ctx, w, pool, th)
	})
	return mux
}

func (r *Registry) writeMetrics(ctx context.Context, w http.ResponseWriter, pool *pgxpool.Pool, th Thresholds) {
	var b strings.Builder

	// Counters, grouped by metric family for valid exposition output.
	families := map[string][]string{}
	r.counters.Range(func(k, v any) bool {
		name := k.(string)
		family := name
		if i := strings.IndexByte(name, '{'); i >= 0 {
			family = name[:i]
		}
		families[family] = append(families[family], fmt.Sprintf("%s %d", name, v.(*atomic.Int64).Load()))
		return true
	})
	names := make([]string, 0, len(families))
	for n := range families {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "# TYPE %s counter\n", n)
		lines := families[n]
		sort.Strings(lines)
		for _, l := range lines {
			b.WriteString(l + "\n")
		}
	}

	// Queue depth straight from the database.
	if pool != nil {
		rows, err := pool.Query(ctx, `select status, count(*) from download_jobs where status in ('pending', 'processing') group by status`)
		if err == nil {
			counts := map[string]int64{"pending": 0, "processing": 0}
			for rows.Next() {
				var s string
				var n int64
				if rows.Scan(&s, &n) == nil {
					counts[s] = n
				}
			}
			rows.Close()
			b.WriteString("# TYPE downly_jobs gauge\n")
			fmt.Fprintf(&b, "downly_jobs{status=\"pending\"} %d\n", counts["pending"])
			fmt.Fprintf(&b, "downly_jobs{status=\"processing\"} %d\n", counts["processing"])
		}
	}

	st := r.Check(ctx, nil, th)
	up := 0
	if st.Telegram == "ok" {
		up = 1
	}
	b.WriteString("# TYPE downly_telegram_up gauge\n")
	fmt.Fprintf(&b, "downly_telegram_up %d\n", up)

	r.mu.Lock()
	now := r.now()
	ids := make([]string, 0, len(r.workers))
	for id := range r.workers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	b.WriteString("# TYPE downly_worker_last_seen_seconds gauge\n")
	for _, id := range ids {
		fmt.Fprintf(&b, "downly_worker_last_seen_seconds{worker=%q} %.0f\n", id, now.Sub(r.workers[id]).Seconds())
	}
	uptime := now.Sub(r.started).Seconds()
	r.mu.Unlock()
	b.WriteString("# TYPE downly_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "downly_uptime_seconds %.0f\n", uptime)

	_, _ = w.Write([]byte(b.String()))
}

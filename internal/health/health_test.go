package health

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var th = Thresholds{WorkerStale: 5 * time.Minute, TelegramStale: 5 * time.Minute}

func newTestRegistry(now *time.Time) *Registry {
	r := New()
	r.now = func() time.Time { return *now }
	r.started = *now
	return r
}

func TestCheckHealthy(t *testing.T) {
	now := time.Unix(10000, 0)
	r := newTestRegistry(&now)
	r.TelegramResult(nil)
	r.WorkerSeen("w1")
	st := r.Check(context.Background(), func(context.Context) error { return nil }, th)
	if !st.OK || st.Telegram != "ok" || st.Workers["w1"] != "ok" {
		t.Fatalf("status = %+v", st)
	}
}

func TestCheckStaleWorker(t *testing.T) {
	now := time.Unix(10000, 0)
	r := newTestRegistry(&now)
	r.TelegramResult(nil)
	r.WorkerSeen("w1")
	now = now.Add(6 * time.Minute)
	r.TelegramResult(nil)
	st := r.Check(context.Background(), nil, th)
	if st.OK || !strings.HasPrefix(st.Workers["w1"], "stale") {
		t.Fatalf("stale worker not reported: %+v", st)
	}
	r.WorkerGone("w1")
	if st := r.Check(context.Background(), nil, th); !st.OK {
		t.Fatalf("stopped worker should not count: %+v", st)
	}
}

func TestCheckTelegramDown(t *testing.T) {
	now := time.Unix(10000, 0)
	r := newTestRegistry(&now)
	if st := r.Check(context.Background(), nil, th); !st.OK || st.Telegram != "starting" {
		t.Fatalf("fresh start should be ok/starting: %+v", st)
	}
	r.TelegramResult(nil)
	now = now.Add(time.Minute)
	r.TelegramResult(errors.New("dial tcp: timeout"))
	if st := r.Check(context.Background(), nil, th); !st.OK {
		t.Fatal("one failed probe within the window is fine")
	}
	now = now.Add(10 * time.Minute)
	st := r.Check(context.Background(), nil, th)
	if st.OK || !strings.Contains(st.Telegram, "dial tcp") {
		t.Fatalf("telegram outage not reported: %+v", st)
	}
}

func TestCheckDatabaseDown(t *testing.T) {
	now := time.Unix(10000, 0)
	r := newTestRegistry(&now)
	st := r.Check(context.Background(), func(context.Context) error { return errors.New("conn refused") }, th)
	if st.OK || st.Database != "conn refused" {
		t.Fatalf("status = %+v", st)
	}
}

func TestHandlerEndpoints(t *testing.T) {
	r := New()
	r.TelegramResult(nil)
	r.WorkerSeen("w1")
	r.Inc(`downly_jobs_finished_total{result="done"}`)
	r.Inc(`downly_jobs_finished_total{result="done"}`)
	r.Inc(`downly_jobs_finished_total{result="failed"}`)
	r.Inc("downly_upload_failures_total")
	srv := httptest.NewServer(r.Handler(nil, th))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	var st Status
	_ = json.NewDecoder(resp.Body).Decode(&st)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !st.OK {
		t.Fatalf("health = %d %+v", resp.StatusCode, st)
	}

	resp, err = http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	body := string(data)
	for _, want := range []string{
		"# TYPE downly_jobs_finished_total counter",
		`downly_jobs_finished_total{result="done"} 2`,
		`downly_jobs_finished_total{result="failed"} 1`,
		"downly_upload_failures_total 1",
		"downly_telegram_up 1",
		`downly_worker_last_seen_seconds{worker="w1"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q\n%s", want, body)
		}
	}
	if strings.Count(body, "# TYPE downly_jobs_finished_total") != 1 {
		t.Error("each metric family must have exactly one TYPE line")
	}
}

func TestHealthReturns503WhenUnhealthy(t *testing.T) {
	r := New()
	r.started = time.Now().Add(-time.Hour) // Telegram never reached
	srv := httptest.NewServer(r.Handler(nil, th))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

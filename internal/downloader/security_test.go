package downloader

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meanii/downly/internal/safeurl"
)

func TestBuildArgsPutsURLAfterDoubleDash(t *testing.T) {
	y := YTDLP{Bin: "yt-dlp"}
	args := y.buildArgs([]string{"--ignore-config", "-f", "best"}, "--exec=touch /tmp/pwned")
	if len(args) < 2 || args[len(args)-2] != "--" || args[len(args)-1] != "--exec=touch /tmp/pwned" {
		t.Fatalf("URL must be the last arg, right after --: %v", args)
	}
}

func TestBuildArgsCookiesBeforeDoubleDash(t *testing.T) {
	cookies := filepath.Join(t.TempDir(), "c.txt")
	if err := os.WriteFile(cookies, []byte("#"), 0o600); err != nil {
		t.Fatal(err)
	}
	y := YTDLP{Bin: "yt-dlp", CookiesFile: cookies}
	args := strings.Join(y.buildArgs([]string{"--ignore-config"}, "https://x.com/a"), " ")
	if !strings.Contains(args, "--cookies "+cookies+" -- https://x.com/a") {
		t.Fatalf("unexpected args: %s", args)
	}
}

func TestDownloadRejectsUnsafeURLs(t *testing.T) {
	y := YTDLP{Bin: "/bin/false"}
	dir := t.TempDir()
	for _, u := range []string{"--exec=id", "http://127.0.0.1/a.jpg", "file:///etc/passwd", "http://169.254.169.254/"} {
		if _, err := y.Download(context.Background(), dir, 1, u, nil); err == nil {
			t.Errorf("Download(%q) should fail", u)
		}
		if _, err := y.DownloadAudio(context.Background(), dir, 1, u, nil); err == nil {
			t.Errorf("DownloadAudio(%q) should fail", u)
		}
		if _, err := y.DownloadWithQuality(context.Background(), dir, 1, u, "q720", nil); err == nil {
			t.Errorf("DownloadWithQuality(%q) should fail", u)
		}
		if _, _, err := y.FetchPlaylist(context.Background(), u, 5); err == nil {
			t.Errorf("FetchPlaylist(%q) should fail", u)
		}
	}
}

func TestDownloadURLRefusesLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("internal"))
	}))
	defer srv.Close()
	y := YTDLP{}
	if _, err := y.downloadURL(context.Background(), t.TempDir(), srv.URL+"/a.jpg", "a", "direct"); !errors.Is(err, safeurl.ErrBlockedHost) {
		t.Fatalf("expected ErrBlockedHost, got %v", err)
	}
}

func TestDownloadURLEnforcesSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		// No Content-Length: forces the streaming limit to kick in.
		w.(http.Flusher).Flush()
		_, _ = w.Write(make([]byte, 2*1024*1024))
	}))
	defer srv.Close()
	// Use a plain client here: the test server is on loopback, which the
	// safe client refuses by design. The size cap is what's under test.
	// The URL is still validated, so swap the host for a public-looking one
	// and route it to the test server through the transport.
	target := srv.Listener.Addr().String()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, target)
		},
	}}
	y := YTDLP{MaxFileSizeMB: 1, HTTPClient: client}
	dir := t.TempDir()
	_, err := y.downloadURL(context.Background(), dir, "http://images.example.com/a.png", "a", "direct")
	if !errors.Is(err, safeurl.ErrTooLarge) {
		t.Fatalf("expected ErrTooLarge, got %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("partial file should be removed, found %d entries", len(entries))
	}
}

func TestSafeFileStem(t *testing.T) {
	tests := map[string]string{
		"abc123":           "abc123",
		"../../etc/passwd": "_.._etc_passwd",
		"":                 "file",
		"..":               "file",
		"a/b\\c":           "a_b_c",
	}
	for in, want := range tests {
		if got := safeFileStem(in); got != want {
			t.Errorf("safeFileStem(%q) = %q, want %q", in, got, want)
		}
		if strings.Contains(safeFileStem(in), "/") {
			t.Errorf("safeFileStem(%q) contains a slash", in)
		}
	}
}

func TestValidQuality(t *testing.T) {
	for _, q := range []string{"q360", "q480", "q720", "q1080", "telegram", "best", "qbest"} {
		if !ValidQuality(q) {
			t.Errorf("%q should be valid", q)
		}
	}
	for _, q := range []string{"", "q4k", "--exec=id #", "audio"} {
		if ValidQuality(q) {
			t.Errorf("%q should be invalid", q)
		}
	}
}

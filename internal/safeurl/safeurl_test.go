package safeurl

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestValidate(t *testing.T) {
	tests := []struct {
		in      string
		wantErr error
	}{
		{"https://www.youtube.com/watch?v=abc", nil},
		{"http://example.com/a.jpg", nil},
		{"https://8.8.8.8/x", nil},
		{"", ErrInvalidURL},
		{"--exec=id", ErrInvalidURL},
		{"-o/etc/passwd", ErrInvalidURL},
		{"ftp://example.com", ErrInvalidURL},
		{"file:///etc/passwd", ErrInvalidURL},
		{"javascript:alert(1)", ErrInvalidURL},
		{"https://", ErrInvalidURL},
		{"https://exa mple.com", ErrInvalidURL},
		{"https://example.com/\nfoo", ErrInvalidURL},
		{"q720:https://example.com", ErrInvalidURL},
		{"https://" + strings.Repeat("a", MaxURLLength), ErrInvalidURL},
		{"http://localhost:8080/health", ErrBlockedHost},
		{"http://foo.localhost/", ErrBlockedHost},
		{"http://metadata.google.internal/", ErrBlockedHost},
		{"http://127.0.0.1/", ErrBlockedHost},
		{"http://10.0.0.5/", ErrBlockedHost},
		{"http://192.168.1.1/", ErrBlockedHost},
		{"http://169.254.169.254/latest/meta-data/", ErrBlockedHost},
		{"http://[::1]/", ErrBlockedHost},
		{"http://[::ffff:127.0.0.1]/", ErrBlockedHost},
		{"http://0.0.0.0/", ErrBlockedHost},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			_, err := Validate(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate(%q) err = %v, want %v", tt.in, err, tt.wantErr)
			}
		})
	}
}

func TestIsPublicAddr(t *testing.T) {
	public := []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"}
	private := []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.0.1", "169.254.169.254", "100.64.0.1", "::1", "fe80::1", "fc00::1", "0.0.0.0", "224.0.0.1"}
	for _, s := range public {
		if !IsPublicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should be public", s)
		}
	}
	for _, s := range private {
		if IsPublicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s should not be public", s)
		}
	}
}

func TestCheckHostLiteral(t *testing.T) {
	if err := CheckHost(context.Background(), "http://127.0.0.1/"); !errors.Is(err, ErrBlockedHost) {
		t.Fatalf("want ErrBlockedHost, got %v", err)
	}
	if err := CheckHost(context.Background(), "https://8.8.8.8/"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer srv.Close()

	c := NewClient(5 * time.Second)
	// Bypass Validate by hitting the dialer directly with the loopback address.
	_, err := c.Get(srv.URL)
	if err == nil || !errors.Is(err, ErrBlockedHost) {
		t.Fatalf("expected blocked dial, got %v", err)
	}
}

func TestClientRefusesRedirectToPrivate(t *testing.T) {
	c := NewClient(5 * time.Second)
	req, _ := http.NewRequest(http.MethodGet, "http://169.254.169.254/", nil)
	if err := c.CheckRedirect(req, []*http.Request{{}}); err == nil {
		t.Fatal("expected redirect to private address to be refused")
	}
}

func TestCopyLimited(t *testing.T) {
	var buf bytes.Buffer
	n, err := CopyLimited(&buf, strings.NewReader("12345"), 5)
	if err != nil || n != 5 {
		t.Fatalf("exact size: n=%d err=%v", n, err)
	}
	buf.Reset()
	_, err = CopyLimited(&buf, strings.NewReader("123456"), 5)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("want ErrTooLarge, got %v", err)
	}
}

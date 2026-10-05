// Package safeurl validates user-supplied URLs and provides an HTTP client
// that refuses to connect to private, loopback or link-local addresses.
package safeurl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// MaxURLLength caps accepted URLs; anything longer is almost certainly abuse.
const MaxURLLength = 2048

var (
	ErrInvalidURL  = errors.New("invalid URL")
	ErrBlockedHost = errors.New("URL points to a private or local address")
	ErrTooLarge    = errors.New("response exceeds size limit")
)

// Validate checks that raw is an absolute http(s) URL with a public-looking
// host. It does not resolve DNS; use CheckHost for that.
func Validate(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > MaxURLLength {
		return nil, ErrInvalidURL
	}
	// Anything starting with "-" could be read as a command-line flag.
	if strings.HasPrefix(raw, "-") {
		return nil, ErrInvalidURL
	}
	if strings.ContainsAny(raw, " \t\r\n\x00") {
		return nil, ErrInvalidURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrInvalidURL
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrInvalidURL
	}
	host := u.Hostname()
	if host == "" {
		return nil, ErrInvalidURL
	}
	if isBlockedHostname(host) {
		return nil, ErrBlockedHost
	}
	if addr, err := netip.ParseAddr(host); err == nil && !IsPublicAddr(addr) {
		return nil, ErrBlockedHost
	}
	return u, nil
}

// CheckHost resolves the URL's host and rejects it if any address is not public.
func CheckHost(ctx context.Context, raw string) error {
	u, err := Validate(raw)
	if err != nil {
		return err
	}
	host := u.Hostname()
	if addr, err := netip.ParseAddr(host); err == nil {
		if !IsPublicAddr(addr) {
			return ErrBlockedHost
		}
		return nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, a := range addrs {
		if !IsPublicAddr(a) {
			return ErrBlockedHost
		}
	}
	return nil
}

func isBlockedHostname(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h == "localhost" || strings.HasSuffix(h, ".localhost") ||
		strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal")
}

// IsPublicAddr reports whether addr is a globally routable unicast address.
func IsPublicAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.IsLoopback() || addr.IsPrivate() || addr.IsUnspecified() ||
		addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsInterfaceLocalMulticast() ||
		addr.IsMulticast() {
		return false
	}
	for _, p := range extraBlocked {
		if p.Contains(addr) {
			return false
		}
	}
	return true
}

var extraBlocked = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("192.0.0.0/24"),  // IETF protocol assignments
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking
	netip.MustParsePrefix("240.0.0.0/4"),   // reserved
	netip.MustParsePrefix("64:ff9b::/96"),  // NAT64 can reach IPv4 private space
	netip.MustParsePrefix("2001:db8::/32"), // documentation
}

// dialControl runs after DNS resolution, right before connect, so it also
// defeats DNS rebinding.
func dialControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if !IsPublicAddr(addr) {
		return ErrBlockedHost
	}
	return nil
}

// NewClient returns an HTTP client that only connects to public addresses.
func NewClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second, Control: dialControl}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			MaxIdleConns:          20,
			IdleConnTimeout:       90 * time.Second,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if _, err := Validate(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}

// CopyLimited copies at most limit bytes from src to dst and returns
// ErrTooLarge if src has more.
func CopyLimited(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	n, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, ErrTooLarge
	}
	return n, nil
}

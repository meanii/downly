package telegram

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCookieJar(t *testing.T) {
	good := "# Netscape HTTP Cookie File\r\n" +
		".instagram.com\tTRUE\t/\tTRUE\t1824898178\tsessionid\tabc\n" +
		"#HttpOnly_.youtube.com\tTRUE\t/\tTRUE\t0\tYSC\tx\n\n"
	jar, err := parseCookieJar([]byte(good))
	if err != nil || jar.count != 2 || !jar.instagramSession || strings.Join(jar.hosts, ",") != "instagram.com,youtube.com" {
		t.Fatalf("got %+v, %v", jar, err)
	}
	loggedOut := ".instagram.com\tTRUE\t/\tTRUE\t0\tcsrftoken\tx\n"
	if jar, err := parseCookieJar([]byte(loggedOut)); err != nil || jar.instagramSession {
		t.Errorf("csrftoken alone is not a session: %+v, %v", jar, err)
	}
	for _, bad := range []string{"", "# only comments\n", "not\ta\tcookie\n", "<html></html>"} {
		if _, err := parseCookieJar([]byte(bad)); err == nil {
			t.Errorf("should reject %q", bad)
		}
	}
}

func TestWriteFileAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".cookies-*"))
	if string(got) != "new" || len(left) != 0 {
		t.Fatalf("content %q, leftovers %v", got, left)
	}
}

package downloader

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cookieCopyDir holds per-invocation copies of the cookies file.
var cookieCopyDir = filepath.Join(os.TempDir(), "downly-cookies")

// cookieCopyMaxAge is how long a copy may linger; no yt-dlp run lasts longer
// than a job timeout, so older copies are leftovers.
const cookieCopyMaxAge = 2 * time.Hour

// cookieCopy returns a private, writable copy of the configured cookies
// file for one yt-dlp run, or "" if none is configured or readable.
//
// yt-dlp rewrites its --cookies file on exit, so passing the original
// crashes it when the file is read-only (e.g. a ":ro" Docker mount), and
// concurrent runs would race on the same file. The original is never
// modified; refresh it by exporting new cookies as before.
func (y YTDLP) cookieCopy() string {
	if y.CookiesFile == "" {
		return ""
	}
	src, err := os.Open(y.CookiesFile)
	if err != nil {
		return ""
	}
	defer src.Close()
	if err := os.MkdirAll(cookieCopyDir, 0o700); err != nil {
		return ""
	}
	sweepCookieCopies()
	dst, err := os.CreateTemp(cookieCopyDir, "cookies-*.txt")
	if err != nil {
		return ""
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		os.Remove(dst.Name())
		return ""
	}
	if err := dst.Close(); err != nil {
		os.Remove(dst.Name())
		return ""
	}
	return dst.Name()
}

// sweepCookieCopies removes copies left behind by finished runs.
func sweepCookieCopies() {
	entries, err := os.ReadDir(cookieCopyDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "cookies-") {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > cookieCopyMaxAge {
			os.Remove(filepath.Join(cookieCopyDir, e.Name()))
		}
	}
}

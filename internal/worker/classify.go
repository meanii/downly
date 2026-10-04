package worker

import (
	"errors"
	"strings"
	"time"

	"github.com/go-telegram/bot"

	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/safeurl"
)

// permanentMarkers are extractor messages that no retry will fix, on top of
// downloader.IsUnavailable.
var permanentMarkers = []string{
	"unsupported url",
	"is not a valid url",
	"http error 404",
	"http error 410",
	"no video or image could be extracted",
	"no video formats found",
	"there is no video in this post",
}

// notMediaMarkers mean the link simply isn't media (an article, a profile
// page...), as opposed to media that failed to download.
var notMediaMarkers = []string{
	"unsupported url",
	"no video or image could be extracted",
	"no video formats found",
	"there is no video in this post",
	"no video could be found",
}

// isNotMedia reports whether err means the link holds nothing downloadable.
func isNotMedia(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, m := range notMediaMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// isPermanent reports whether retrying err is pointless.
func isPermanent(err error) bool {
	if err == nil {
		return false
	}
	if downloader.IsUnavailable(err) || errors.Is(err, safeurl.ErrInvalidURL) ||
		errors.Is(err, safeurl.ErrBlockedHost) || errors.Is(err, safeurl.ErrTooLarge) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, m := range permanentMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// errForbidden is what Telegram returns when the user blocked the bot.
var errForbidden = bot.ErrorForbidden

// isPermanentUploadErr reports Telegram errors that a re-upload won't fix
// (bad request, bot blocked by user, chat gone).
func isPermanentUploadErr(err error) bool {
	return errors.Is(err, bot.ErrorBadRequest) || errors.Is(err, bot.ErrorForbidden) ||
		errors.Is(err, bot.ErrorNotFound) || errors.Is(err, bot.ErrorUnauthorized)
}

// retryDelay is the backoff before attempt retry+1: 30s, 2m, 8m, ... capped at 30m.
func retryDelay(retry int) time.Duration {
	d := 30 * time.Second
	for i := 0; i < retry && d < 30*time.Minute; i++ {
		d *= 4
	}
	if d > 30*time.Minute {
		d = 30 * time.Minute
	}
	return d
}

// friendlyError turns raw yt-dlp output into something short for users.
func friendlyError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// yt-dlp prints the useful part on the last "ERROR:" line.
	if i := strings.LastIndex(msg, "ERROR:"); i >= 0 {
		msg = strings.TrimSpace(msg[i+len("ERROR:"):])
		if j := strings.IndexByte(msg, '\n'); j >= 0 {
			msg = msg[:j]
		}
	}
	return truncate(msg)
}

// Package media holds the types shared by the cache, worker and handlers
// for media already uploaded to Telegram.
package media

import (
	"net/url"
	"sort"
	"strings"

	"github.com/go-telegram/bot/models"
)

// Kind is how a file was sent to Telegram.
type Kind string

const (
	Video     Kind = "video"
	Audio     Kind = "audio"
	Photo     Kind = "photo"
	Document  Kind = "document"
	Animation Kind = "animation"
)

// Item is one file Telegram already stores, re-sendable by FileID.
type Item struct {
	Kind   Kind   `json:"type"`
	FileID string `json:"file_id"`
}

// Meta describes cached media for captions and audio tags.
type Meta struct {
	Title     string
	Performer string
	Platform  string
	Duration  int
	SizeBytes int64
}

// ItemFromMessage extracts the file a sent message carries.
func ItemFromMessage(m *models.Message) (Item, bool) {
	switch {
	case m == nil:
	case m.Animation != nil:
		return Item{Animation, m.Animation.FileID}, true
	case m.Video != nil:
		return Item{Video, m.Video.FileID}, true
	case m.Audio != nil:
		return Item{Audio, m.Audio.FileID}, true
	case len(m.Photo) > 0:
		// Sizes are ascending; the largest is the original.
		return Item{Photo, m.Photo[len(m.Photo)-1].FileID}, true
	case m.Document != nil:
		return Item{Document, m.Document.FileID}, true
	}
	return Item{}, false
}

// trackingParams are query parameters that change per share but not the
// content, so they must not split the cache. Keep this list conservative:
// stripping a meaningful parameter would serve the wrong cached file.
var trackingParams = map[string]bool{
	"si": true, "feature": true, "igsh": true, "igshid": true,
	"fbclid": true, "gclid": true, "ref_src": true, "ref_url": true,
	"share_id": true, "is_from_webapp": true, "sender_device": true, "web_id": true,
}

// CanonicalURL normalizes a URL for cache lookups: lowercase scheme and
// host, no "www."/"m." prefix, no fragment, no tracking parameters, sorted
// query, no trailing slash. It returns raw unchanged if it does not parse.
func CanonicalURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return raw
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme == "http" {
		u.Scheme = "https"
	}
	host := strings.ToLower(u.Host)
	for _, p := range []string{"www.", "m.", "mobile."} {
		host = strings.TrimPrefix(host, p)
	}
	if host == "youtu.be" {
		// youtu.be/<id> and youtube.com/watch?v=<id> are the same video.
		if id := strings.Trim(u.Path, "/"); id != "" {
			q := u.Query()
			q.Set("v", id)
			u.RawQuery = q.Encode()
			u.Path = "/watch"
			host = "youtube.com"
		}
	}
	u.Host = host
	u.Fragment = ""
	q := u.Query()
	for k := range q {
		if trackingParams[strings.ToLower(k)] || strings.HasPrefix(strings.ToLower(k), "utm_") {
			q.Del(k)
		}
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		vals := q[k]
		sort.Strings(vals)
		for j, v := range vals {
			if i > 0 || j > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k) + "=" + url.QueryEscape(v))
		}
	}
	u.RawQuery = b.String()
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String()
}

// CacheKey identifies a download: the same content in the same mode,
// quality and variant (e.g. clip range) maps to the same key.
func CacheKey(rawURL, mode, quality, variant string) string {
	return CanonicalURL(rawURL) + "|" + mode + "|" + quality + "|" + variant
}

package telegram

import (
	"context"
	"strings"

	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/i18n"
)

// maxClipSeconds caps /clip and "url start-end" sections.
const maxClipSeconds = 20 * 60

// urlRequest is a URL found in a message, optionally with a time range
// written right after it ("https://... 1:20-2:05").
type urlRequest struct {
	url  string
	clip *downloader.Range
}

// extractRequests finds URLs (with mode prefixes) and any range following them.
func extractRequests(text string) []urlRequest {
	words := strings.Fields(text)
	var out []urlRequest
	for i, word := range words {
		clean, prefix := stripModePrefix(word)
		normalized := normalizeURL(clean)
		if !looksLikeURL(normalized) {
			continue
		}
		req := urlRequest{url: prefix + normalized}
		if i+1 < len(words) {
			if r, err := downloader.ParseRange(words[i+1]); err == nil {
				req.clip = &r
			}
		}
		out = append(out, req)
	}
	return out
}

// checkClip validates a clip length, returning the message to show if it
// is not acceptable.
func checkClip(lang i18n.Lang, r *downloader.Range, gif bool) (string, bool) {
	if r == nil {
		return "", true
	}
	if gif && r.Seconds() > downloader.MaxGIFSeconds {
		return i18n.T(lang, "gif_too_long", downloader.MaxGIFSeconds), false
	}
	if r.Seconds() > maxClipSeconds {
		return i18n.T(lang, "clip_too_long", maxClipSeconds/60), false
	}
	return "", true
}

// cmdClip handles "/clip <url> <start-end>".
func (h *handler) cmdClip(ctx context.Context, r *request) {
	reqs := extractRequests(r.args)
	if len(reqs) == 0 {
		h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "clip_usage"))
		return
	}
	if reqs[0].clip == nil {
		h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "clip_invalid"))
		return
	}
	h.downloadAll(ctx, r.msg, reqs[:1], r.msg.ID, false)
}

// cmdGIF handles "/gif <url> [start-end]".
func (h *handler) cmdGIF(ctx context.Context, r *request) {
	reqs := extractRequests(r.args)
	if len(reqs) == 0 {
		h.reply(ctx, r.msg.Chat.ID, i18n.T(r.lang, "gif_usage", downloader.DefaultGIFSeconds, downloader.MaxGIFSeconds))
		return
	}
	h.downloadAll(ctx, r.msg, reqs[:1], r.msg.ID, true)
}

// clipVariant is the cache-key variant for a clip or GIF request.
func clipVariant(clip *downloader.Range, gif bool) string {
	switch {
	case gif && clip != nil:
		return "gif:" + clip.String()
	case gif:
		return "gif:" + downloader.Range{End: downloader.DefaultGIFSeconds}.String()
	case clip != nil:
		return "clip:" + clip.String()
	}
	return ""
}

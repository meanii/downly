package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// maxCookieFileBytes bounds a /cookies upload; real jars are a few KB.
const maxCookieFileBytes = 1 << 20

// cmdCookies replaces the yt-dlp cookies file with a Netscape cookies.txt an
// admin sent (as the document's caption, or by replying to it), so an expired
// login can be fixed from Telegram without shell access. yt-dlp only ever
// sees private copies of the file, so nothing else writes to it.
func (h *handler) cmdCookies(ctx context.Context, r *request) {
	path := h.cfg.Downly.Services.YTDLP.CookiesFile
	if path == "" {
		h.reply(ctx, r.msg.Chat.ID, "services.ytdl.cookies_file is not configured.")
		return
	}
	doc := r.msg.Document
	if doc == nil && r.msg.ReplyToMessage != nil {
		doc = r.msg.ReplyToMessage.Document
	}
	if doc == nil {
		h.reply(ctx, r.msg.Chat.ID, "Send a cookies.txt (Netscape format) with the caption /cookies, or reply /cookies to one.")
		return
	}
	if doc.FileSize > maxCookieFileBytes {
		h.reply(ctx, r.msg.Chat.ID, "That file is too large for a cookies file.")
		return
	}
	data, err := h.downloadDocument(ctx, doc)
	if err != nil {
		h.log.Error("download cookies file failed", "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Failed to download the file from Telegram.")
		return
	}
	jar, err := parseCookieJar(data)
	if err != nil {
		h.reply(ctx, r.msg.Chat.ID, "Not a Netscape cookies file: "+err.Error())
		return
	}
	if err := writeFileAtomic(path, data); err != nil {
		h.log.Error("write cookies file failed", "path", path, "error", err)
		h.reply(ctx, r.msg.Chat.ID, "Failed to write the cookies file: "+err.Error())
		return
	}
	h.log.Info("cookies file replaced", "by", r.msg.From.ID, "cookies", jar.count, "hosts", jar.hosts)
	text := fmt.Sprintf("Saved %d cookies for %s.", jar.count, strings.Join(jar.hosts, ", "))
	if !jar.instagramSession {
		text += "\n⚠️ No Instagram sessionid: Instagram downloads will run logged out."
	}
	h.reply(ctx, r.msg.Chat.ID, text)
}

func (h *handler) downloadDocument(ctx context.Context, doc *models.Document) ([]byte, error) {
	f, err := h.b.GetFile(ctx, &bot.GetFileParams{FileID: doc.FileID})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.b.FileDownloadLink(f), nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram file download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxCookieFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCookieFileBytes {
		return nil, errors.New("file larger than declared")
	}
	return data, nil
}

type cookieJar struct {
	count            int
	hosts            []string // registrable domains, sorted
	instagramSession bool     // a non-empty instagram.com sessionid is present
}

// parseCookieJar validates Netscape cookies.txt data: every cookie line has
// 7 tab-separated fields. "#HttpOnly_" lines (written by curl and browser
// exporters) are cookies, not comments.
func parseCookieJar(data []byte) (cookieJar, error) {
	var jar cookieJar
	seen := map[string]bool{}
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) != 7 {
			return jar, fmt.Errorf("line %d has %d fields, want 7 tab-separated", i+1, len(f))
		}
		jar.count++
		host := strings.TrimPrefix(f[0], ".")
		if labels := strings.Split(host, "."); len(labels) > 2 {
			host = strings.Join(labels[len(labels)-2:], ".")
		}
		seen[host] = true
		if host == "instagram.com" && f[5] == "sessionid" && f[6] != "" {
			jar.instagramSession = true
		}
	}
	if jar.count == 0 {
		return jar, errors.New("no cookies found")
	}
	for h := range seen {
		jar.hosts = append(jar.hosts, h)
	}
	sort.Strings(jar.hosts)
	return jar, nil
}

// writeFileAtomic replaces path via a temp file and rename, so a download
// starting mid-write never reads a truncated jar.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".cookies-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o600)
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

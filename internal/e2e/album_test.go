package e2e

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/meanii/downly/internal/db"
)

type groupItem struct {
	Type    string `json:"type"`
	Media   string `json:"media"`
	Caption string `json:"caption"`
}

func (e *env) mediaGroups(chatID int64) [][]groupItem {
	var out [][]groupItem
	for _, c := range e.api.CallsTo("sendMediaGroup") {
		if c.Fields["chat_id"] != fmt.Sprint(chatID) {
			continue
		}
		var items []groupItem
		if err := json.Unmarshal([]byte(c.Fields["media"]), &items); err != nil {
			e.t.Fatalf("bad media JSON: %v", err)
		}
		out = append(out, items)
	}
	return out
}

// A carousel post on an album site arrives as one album with every item,
// the caption on the first; a repeat request re-sends the album by file ID.
func TestE2ECarouselAsAlbum(t *testing.T) {
	e := newEnv(t)
	const user, other = 401, 402
	const post = "https://1.1.1.1/p/carousel"

	e.send(private(user), user, post)
	if j := e.waitJob(user); j.Status != db.StatusDone {
		t.Fatalf("job = %+v", j)
	}
	groups := e.mediaGroups(user)
	if len(groups) != 1 || len(groups[0]) != 3 {
		t.Fatalf("groups = %+v", groups)
	}
	g := groups[0]
	if g[0].Type != "photo" || g[1].Type != "video" || g[2].Type != "photo" {
		t.Fatalf("item types = %+v", g)
	}
	if g[0].Caption == "" || g[1].Caption != "" {
		t.Fatalf("caption should be on the first item only: %+v", g)
	}
	call := e.api.CallsTo("sendMediaGroup")[0]
	for i := 0; i < 3; i++ {
		f, ok := call.Files[fmt.Sprintf("file%d", i)]
		if !ok || string(f.Data) != fmt.Sprintf("ITEM-%d", i+1) {
			t.Fatalf("attachment %d = %+v", i, f)
		}
	}
	if len(e.sendsTo("sendPhoto", user)) != 0 {
		t.Fatal("album must not also be sent item by item")
	}
	if n := len(e.cacheEntry("", post).Items); n != 3 {
		t.Fatalf("cached items = %d", n)
	}

	e.send(private(other), other, post)
	again := e.mediaGroups(other)
	if len(again) != 1 || len(again[0]) != 3 || strings.HasPrefix(again[0][0].Media, "attach://") {
		t.Fatalf("cached album resend = %+v", again)
	}
	if e.downloads() != 1 {
		t.Fatalf("downloads = %d", e.downloads())
	}
}

// Off the album allowlist, a playlist-like link is fetched as a single item.
func TestE2ENonAlbumHostSendsSingleItem(t *testing.T) {
	e := newEnv(t)
	const user = 403
	e.send(private(user), user, "https://1.0.0.1/p/carousel")
	if j := e.waitJob(user); j.Status != db.StatusDone {
		t.Fatalf("job = %+v", j)
	}
	if len(e.mediaGroups(user)) != 0 || len(e.sendsTo("sendPhoto", user)) != 1 {
		t.Fatalf("expected one photo, got groups=%v photos=%v", e.mediaGroups(user), e.sendsTo("sendPhoto", user))
	}
}

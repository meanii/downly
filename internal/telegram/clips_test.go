package telegram

import (
	"testing"

	"github.com/meanii/downly/internal/downloader"
	"github.com/meanii/downly/internal/i18n"
)

func TestExtractRequests(t *testing.T) {
	reqs := extractRequests("watch https://youtu.be/a 1:20-2:05 and q720:https://youtu.be/b then https://youtu.be/c notarange")
	if len(reqs) != 3 {
		t.Fatalf("got %d requests", len(reqs))
	}
	if reqs[0].url != "https://youtu.be/a" || reqs[0].clip == nil || *reqs[0].clip != (downloader.Range{Start: 80, End: 125}) {
		t.Errorf("first = %+v", reqs[0])
	}
	if reqs[1].url != "q720:https://youtu.be/b" || reqs[1].clip != nil {
		t.Errorf("second = %+v", reqs[1])
	}
	if reqs[2].clip != nil {
		t.Errorf("third = %+v", reqs[2])
	}
}

func TestCheckClip(t *testing.T) {
	if _, ok := checkClip(i18n.EN, nil, true); !ok {
		t.Error("no range is fine")
	}
	if msg, ok := checkClip(i18n.EN, &downloader.Range{Start: 0, End: 31}, true); ok || msg == "" {
		t.Error("31s GIF should be refused")
	}
	if _, ok := checkClip(i18n.EN, &downloader.Range{Start: 0, End: 31}, false); !ok {
		t.Error("31s clip is fine")
	}
	if _, ok := checkClip(i18n.EN, &downloader.Range{Start: 0, End: maxClipSeconds + 1}, false); ok {
		t.Error("overlong clip should be refused")
	}
}

func TestClipVariant(t *testing.T) {
	r := &downloader.Range{Start: 5, End: 9}
	seen := map[string]bool{}
	for _, v := range []string{clipVariant(nil, false), clipVariant(r, false), clipVariant(r, true), clipVariant(nil, true)} {
		if seen[v] {
			t.Fatalf("duplicate variant %q", v)
		}
		seen[v] = true
	}
	if clipVariant(nil, false) != "" {
		t.Fatal("plain downloads have no variant")
	}
}

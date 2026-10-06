package media

import "testing"

func TestPlatformFromURL(t *testing.T) {
	cases := map[string]string{
		"https://www.instagram.com/reel/abc/?stkn=x": "instagram",
		"https://youtu.be/abc":                       "youtube",
		"https://m.youtube.com/watch?v=a":            "youtube",
		"https://x.com/u/status/1":                   "twitter",
		"https://vm.tiktok.com/ZM123/":               "tiktok",
		"https://old.reddit.com/r/x":                 "reddit",
		"https://www.threads.net/@u/post/1":          "threads",
		"https://1.1.1.1/watch":                      "",
		"not a url":                                  "",
	}
	for in, want := range cases {
		if got := PlatformFromURL(in); got != want {
			t.Errorf("PlatformFromURL(%q) = %q, want %q", in, got, want)
		}
	}
}

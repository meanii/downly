package downloader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIsAlbumURL(t *testing.T) {
	y := YTDLP{}
	for u, want := range map[string]bool{
		"https://www.instagram.com/p/abc/":          true,
		"https://instagram.com/p/abc/":              true,
		"https://x.com/u/status/1":                  true,
		"https://www.tiktok.com/@u/photo/1":         true,
		"https://www.youtube.com/watch?v=a&list=PL": false,
		"https://notinstagram.com/p/abc":            false,
		"https://example.com/instagram.com":         false,
	} {
		if got := y.isAlbumURL(u); got != want {
			t.Errorf("isAlbumURL(%q) = %v, want %v", u, got, want)
		}
	}
	custom := YTDLP{AlbumHosts: []string{"example.org"}}
	if !custom.isAlbumURL("https://cdn.example.org/x") || custom.isAlbumURL("https://instagram.com/p/x") {
		t.Error("custom AlbumHosts should replace the defaults")
	}
}

func TestPlaylistArgs(t *testing.T) {
	y := YTDLP{}
	if got := strings.Join(y.playlistArgs("https://www.youtube.com/watch?v=a&list=PL"), " "); got != "--no-playlist" {
		t.Errorf("youtube args = %q", got)
	}
	if got := strings.Join(y.playlistArgs("https://instagram.com/p/x"), " "); got != "--yes-playlist --playlist-items 1:10" {
		t.Errorf("instagram args = %q", got)
	}
}

func TestFindOutputFilesAlbumOrder(t *testing.T) {
	dir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	// Written out of name order: b, a, c. Plus files that must be ignored.
	for i, name := range []string{"b.jpg", "a.mp4", "c.jpg"} {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, []byte(name), 0o644)
		mt := base.Add(time.Duration(i) * time.Second)
		_ = os.Chtimes(p, mt, mt)
	}
	_ = os.WriteFile(filepath.Join(dir, "thumbnail.jpg"), []byte("t"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "x.part"), []byte("p"), 0o644)

	res, err := findOutputFiles(dir, "instagram", true)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, it := range res.Items() {
		names = append(names, it.FileName)
	}
	if strings.Join(names, ",") != "b.jpg,a.mp4,c.jpg" {
		t.Fatalf("items = %v", names)
	}
	if res.Media != MediaPhoto || res.More[0].Media != MediaVideo {
		t.Fatalf("media types: %v %v", res.Media, res.More[0].Media)
	}

	single, _ := findOutputFiles(dir, "x", false)
	if len(single.More) != 0 || single.FileName != "b.jpg" {
		t.Fatalf("non-album should return just the first file: %+v", single)
	}
}

func TestFindOutputFilesCapsAlbum(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < MaxAlbumItems+5; i++ {
		_ = os.WriteFile(filepath.Join(dir, strings.Repeat("a", i+1)+".jpg"), []byte("x"), 0o644)
	}
	res, err := findOutputFiles(dir, "x", true)
	if err != nil || len(res.Items()) != MaxAlbumItems {
		t.Fatalf("items = %d, err = %v", len(res.Items()), err)
	}
}

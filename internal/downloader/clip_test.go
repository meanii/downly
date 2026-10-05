package downloader

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseRange(t *testing.T) {
	ok := map[string]Range{
		"1:20-2:05":       {80, 125},
		"0:00:10-0:00:25": {10, 25},
		"80-125":          {80, 125},
		"0:05–0:12":       {5, 12}, // en dash, as phones often type it
		"1:00:00-1:00:30": {3600, 3630},
		" 10 - 20 ":       {10, 20},
		"0-3600":          {0, 3600},
		"59:59-1:00:00":   {3599, 3600},
	}
	for in, want := range ok {
		got, err := ParseRange(in)
		if err != nil || got != want {
			t.Errorf("ParseRange(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "abc", "2:05-1:20", "10-10", "1:75-2:00", "1:2:3:4-5", "1:20", "-5", "a-b"} {
		if _, err := ParseRange(in); !errors.Is(err, ErrBadRange) {
			t.Errorf("ParseRange(%q) should fail, got %v", in, err)
		}
	}
}

func TestSectionArgs(t *testing.T) {
	got := strings.Join(sectionArgs(Range{80, 125}), " ")
	if got != "--download-sections *80-125 --force-keyframes-at-cuts" {
		t.Fatalf("got %q", got)
	}
}

func TestDownloadGIFRejectsLongRange(t *testing.T) {
	y := YTDLP{Bin: "/bin/false"}
	_, err := y.DownloadGIF(context.Background(), t.TempDir(), 1, "https://8.8.8.8/v", &Range{0, MaxGIFSeconds + 1}, nil)
	if !errors.Is(err, ErrBadRange) {
		t.Fatalf("want ErrBadRange, got %v", err)
	}
}

// TestToAnimationRealFFmpeg converts a synthetic clip with audio and checks
// the result is a silent H.264 MP4 of the requested length.
func TestToAnimationRealFFmpeg(t *testing.T) {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skip(bin + " not installed")
		}
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30:duration=8",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=8",
		"-c:v", "libx264", "-preset", "ultrafast", "-c:a", "aac", "-shortest", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	out := filepath.Join(dir, "out.mp4")
	y := YTDLP{}
	if err := y.toAnimation(context.Background(), src, out, 4); err != nil {
		t.Fatal(err)
	}
	streams, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "stream=codec_type,width", "-of", "csv=p=0", out).Output()
	if err != nil {
		t.Fatal(err)
	}
	s := strings.TrimSpace(string(streams))
	if strings.Contains(s, "audio") || !strings.HasPrefix(s, "video,480") {
		t.Fatalf("streams = %q, want a single 480px video stream", s)
	}
	if d, err := y.probeDuration(context.Background(), out); err != nil || d < 3.5 || d > 4.5 {
		t.Fatalf("duration = %v (%v), want ~4s", d, err)
	}
	if fi, _ := os.Stat(out); fi.Size() == 0 {
		t.Fatal("empty output")
	}
}

func TestClockAndClipTitle(t *testing.T) {
	if clock(65) != "1:05" || clock(3725) != "1:02:05" {
		t.Fatal("clock")
	}
	if clipTitle("Song", Range{80, 125}) != "Song (1:20-2:05)" || clipTitle("", Range{1, 2}) != "" {
		t.Fatal("clipTitle")
	}
}

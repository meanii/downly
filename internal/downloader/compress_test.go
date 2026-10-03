package downloader

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPlanCompression(t *testing.T) {
	const mb = 1024 * 1024
	tests := []struct {
		name       string
		limit      int64
		duration   float64
		wantOK     bool
		wantHeight int
	}{
		{"short clip fits at 1080p", 50 * mb, 60, true, 1080},
		{"10 min fits at 480p", 50 * mb, 600, true, 480},
		{"30 min drops to 360p", 50 * mb, 1800, true, 360},
		{"40 min is too long for 50MB", 50 * mb, 2400, false, 0},
		{"3h movie cannot fit", 50 * mb, 3 * 3600, false, 0},
		{"unknown duration", 50 * mb, 0, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := planCompression(tt.limit, tt.duration, 1.0)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (plan %+v)", ok, tt.wantOK, p)
			}
			if !ok {
				return
			}
			if p.MaxHeight != tt.wantHeight {
				t.Errorf("height = %d, want %d (plan %+v)", p.MaxHeight, tt.wantHeight, p)
			}
			// The planned bitrate must fit the budget.
			bytes := float64(p.VideoKbps+p.AudioKbps) * 1000 / 8 * tt.duration
			if bytes > float64(tt.limit) {
				t.Errorf("plan %+v produces %.0f bytes > limit %d", p, bytes, tt.limit)
			}
		})
	}
}

func TestPlanCompressionScaleReducesBitrate(t *testing.T) {
	a, _ := planCompression(50*1024*1024, 300, 1.0)
	b, _ := planCompression(50*1024*1024, 300, 0.8)
	if b.VideoKbps >= a.VideoKbps {
		t.Fatalf("scaled plan should use less bitrate: %+v vs %+v", b, a)
	}
}

func TestCheckSizeUsesDownloadCap(t *testing.T) {
	y := YTDLP{MaxFileSizeMB: 50, MaxDownloadMB: 200}
	if err := y.checkSize(mediaInfo{FilesizeApprox: 120 * 1024 * 1024}); err != nil {
		t.Fatalf("120MB is compressible and should be allowed: %v", err)
	}
	err := y.checkSize(mediaInfo{Filesize: 300 * 1024 * 1024})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("300MB should be rejected with ErrTooLarge, got %v", err)
	}
}

func TestSizeArgsDefaultsToFourTimesLimit(t *testing.T) {
	y := YTDLP{MaxFileSizeMB: 50}
	args := y.sizeArgs()
	if len(args) != 2 || args[0] != "--max-filesize" || args[1] != "209715200" {
		t.Fatalf("got %v", args)
	}
}

// TestCompressToFitRealFFmpeg encodes a synthetic clip well above a 1MB limit
// and checks the output fits. Skipped when ffmpeg is not installed.
func TestCompressToFitRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30:duration=12",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=12",
		"-c:v", "libx264", "-preset", "ultrafast", "-b:v", "3000k",
		"-c:a", "aac", "-shortest", "-y", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate source: %v\n%s", err, out)
	}
	si, _ := os.Stat(src)
	const limit = 1024 * 1024
	if si.Size() <= limit {
		t.Fatalf("source %d bytes is not above the 1MB limit; test is not meaningful", si.Size())
	}

	y := YTDLP{MaxFileSizeMB: 1}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Pass duration 0 to also exercise the ffprobe path.
	got := y.compressToFit(context.Background(), log, src, 0)
	if got == src {
		t.Fatal("expected a compressed file, got the original path")
	}
	ci, err := os.Stat(got)
	if err != nil {
		t.Fatal(err)
	}
	if ci.Size() > limit {
		t.Fatalf("compressed file is %d bytes, limit %d", ci.Size(), limit)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("original should be removed after successful compression")
	}
	if d, err := y.probeDuration(context.Background(), got); err != nil || d < 11 {
		t.Fatalf("compressed output looks broken: duration=%v err=%v", d, err)
	}
}

func TestCompressToFitLeavesSmallFilesAlone(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.mp4")
	if err := os.WriteFile(p, []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	y := YTDLP{MaxFileSizeMB: 1, FFmpegBin: "/nonexistent"}
	if got := y.compressToFit(context.Background(), slog.Default(), p, 10); got != p {
		t.Fatalf("got %q", got)
	}
}

package downloader

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/meanii/downly/internal/safeurl"
)

// Range is a section of a video in whole seconds, End > Start.
type Range struct {
	Start, End int
}

func (r Range) Seconds() int { return r.End - r.Start }

// String renders the range as yt-dlp section syntax, e.g. "80-125".
func (r Range) String() string { return fmt.Sprintf("%d-%d", r.Start, r.End) }

var (
	rangeRE   = regexp.MustCompile(`^(\d{1,2}(?::\d{1,2}){0,2})\s*[-–—]\s*(\d{1,2}(?::\d{1,2}){0,2})$`)
	secondsRE = regexp.MustCompile(`^(\d+)\s*[-–—]\s*(\d+)$`)
)

// ParseRange parses "1:20-2:05", "0:00:10-0:00:25" or "80-125" (seconds).
func ParseRange(s string) (Range, error) {
	s = strings.TrimSpace(s)
	if m := secondsRE.FindStringSubmatch(s); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		return validRange(a, b)
	}
	m := rangeRE.FindStringSubmatch(s)
	if m == nil {
		return Range{}, ErrBadRange
	}
	a, err1 := parseClock(m[1])
	b, err2 := parseClock(m[2])
	if err1 != nil || err2 != nil {
		return Range{}, ErrBadRange
	}
	return validRange(a, b)
}

// ErrBadRange is returned for unparseable or empty ranges.
var ErrBadRange = errors.New("invalid time range")

func validRange(a, b int) (Range, error) {
	if b <= a {
		return Range{}, ErrBadRange
	}
	return Range{Start: a, End: b}, nil
}

func parseClock(s string) (int, error) {
	parts := strings.Split(s, ":")
	total := 0
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0, err
		}
		if i > 0 && n >= 60 {
			return 0, ErrBadRange
		}
		total = total*60 + n
	}
	return total, nil
}

// MaxGIFSeconds caps GIF length; DefaultGIFSeconds is used without a range.
const (
	MaxGIFSeconds     = 30
	DefaultGIFSeconds = 10
)

// runYTDLP runs yt-dlp with progress reporting and returns its error output
// on failure.
func (y YTDLP) runYTDLP(ctx context.Context, log *slog.Logger, args []string, url string, onProgress func(string, int)) error {
	cmd := y.command(ctx, args, url)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	stdoutCh := make(chan []string, 1)
	stderrCh := make(chan []string, 1)
	go func() { stdoutCh <- readPipe(stdout, onProgress) }()
	go func() { stderrCh <- readPipe(stderr, nil) }()
	err = cmd.Wait()
	combined := append(<-stdoutCh, <-stderrCh...)
	if err != nil {
		out := strings.TrimSpace(strings.Join(combined, "\n"))
		log.Error("yt-dlp failed", "output", out, "error", err)
		return fmt.Errorf("yt-dlp failed: %s", out)
	}
	return nil
}

// sectionArgs downloads only part of a video. Cutting at keyframes keeps the
// clip accurate at the cost of re-encoding the edges.
func sectionArgs(r Range) []string {
	return []string{"--download-sections", "*" + r.String(), "--force-keyframes-at-cuts"}
}

// DownloadClip downloads only section r of a video, at quality if set.
func (y YTDLP) DownloadClip(ctx context.Context, workDir string, jobID int64, url, quality string, r Range, onProgress func(string, int)) (*Result, error) {
	log := y.logger().With("component", "downloader", "job_id", jobID, "url", url, "clip", r.String())
	if err := safeurl.CheckHost(ctx, url); err != nil {
		return nil, err
	}
	jobDir := filepath.Join(workDir, fmt.Sprintf("job-%d", jobID))
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return nil, err
	}
	// No metadata size check: the full video may be huge while the clip is small.
	meta := y.fetchMetadata(ctx, log, url)

	args := []string{
		"--ignore-config", "--newline", "--no-playlist",
		"-f", qualityFormat(quality),
		"--merge-output-format", "mp4",
		"-o", filepath.Join(jobDir, "%(id)s.%(ext)s"),
	}
	args = append(args, sectionArgs(r)...)
	args = append(args, y.sizeArgs()...)
	if onProgress != nil {
		onProgress("Starting download", 5)
	}
	if err := y.runYTDLP(ctx, log, args, url, onProgress); err != nil {
		return nil, err
	}
	if onProgress != nil {
		onProgress("Finalizing file", 98)
	}
	res, err := findOutputFile(jobDir, meta.Platform)
	if err != nil {
		return nil, err
	}
	res.FilePath = y.compressToFit(ctx, log, res.FilePath, float64(r.Seconds()))
	res.Title = clipTitle(meta.Title, r)
	res.Performer = meta.performer()
	res.Duration = r.Seconds()
	res.FileName = friendlyFileName(res.Title, meta.ID, filepath.Base(res.FilePath))
	res.Media = MediaVideo
	return res, nil
}

// DownloadGIF turns section r (or the first DefaultGIFSeconds) of a video
// into a silent, looping MP4, which Telegram shows as a GIF.
func (y YTDLP) DownloadGIF(ctx context.Context, workDir string, jobID int64, url string, r *Range, onProgress func(string, int)) (*Result, error) {
	section := Range{0, DefaultGIFSeconds}
	if r != nil {
		section = *r
	}
	if section.Seconds() > MaxGIFSeconds {
		return nil, fmt.Errorf("%w: GIFs can be at most %d seconds", ErrBadRange, MaxGIFSeconds)
	}
	res, err := y.DownloadClip(ctx, workDir, jobID, url, "q480", section, onProgress)
	if err != nil {
		return nil, err
	}
	log := y.logger().With("component", "downloader", "job_id", jobID)
	if onProgress != nil {
		onProgress("Converting to GIF", 97)
	}
	out := strings.TrimSuffix(res.FilePath, filepath.Ext(res.FilePath)) + "_gif.mp4"
	if err := y.toAnimation(ctx, res.FilePath, out, section.Seconds()); err != nil {
		log.Error("gif conversion failed", "error", err)
		return nil, fmt.Errorf("gif conversion failed: %w", err)
	}
	_ = os.Remove(res.FilePath)
	res.FilePath = out
	res.FileName = strings.TrimSuffix(res.FileName, filepath.Ext(res.FileName)) + ".mp4"
	res.Media = MediaAnimation
	return res, nil
}

// toAnimation re-encodes in to a small silent H.264 MP4 suitable for
// Telegram animations: no audio, at most 480px wide, 15 fps.
func (y YTDLP) toAnimation(ctx context.Context, in, out string, seconds int) error {
	cmd := exec.CommandContext(ctx, y.ffmpegBin(),
		"-hide_banner", "-loglevel", "error",
		"-i", in,
		"-t", strconv.Itoa(seconds),
		"-an",
		"-vf", "fps=15,scale='min(480,iw)':-2",
		"-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		"-y", out)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, truncateOutput(string(output)))
	}
	return nil
}

func clipTitle(title string, r Range) string {
	if title == "" {
		return ""
	}
	return fmt.Sprintf("%s (%s-%s)", title, clock(r.Start), clock(r.End))
}

// clock renders seconds as m:ss or h:mm:ss.
func clock(sec int) string {
	if sec >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}

func (y YTDLP) logger() *slog.Logger {
	if y.Logger != nil {
		return y.Logger
	}
	return slog.Default()
}

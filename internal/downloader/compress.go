package downloader

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// compressionPlan describes an ffmpeg encode sized to fit a byte budget.
type compressionPlan struct {
	VideoKbps int
	AudioKbps int
	MaxHeight int
}

// sizeHeadroom leaves room for container overhead and rate-control overshoot.
const sizeHeadroom = 0.90

// minVideoKbps is the lowest bitrate we'll encode at; below this the result
// is unwatchable, so we'd rather tell the user to pick a lower quality.
const minVideoKbps = 120

// planCompression picks bitrates and a resolution cap so a video of the given
// duration fits in limitBytes. ok is false if no watchable encode fits.
func planCompression(limitBytes int64, durationSec float64, scale float64) (compressionPlan, bool) {
	if limitBytes <= 0 || durationSec <= 0 {
		return compressionPlan{}, false
	}
	totalKbps := int(float64(limitBytes) * 8 * sizeHeadroom * scale / durationSec / 1000)
	audio := 64
	switch {
	case totalKbps > 1500:
		audio = 128
	case totalKbps > 600:
		audio = 96
	}
	video := totalKbps - audio
	if video < minVideoKbps {
		return compressionPlan{}, false
	}
	height := 360
	switch {
	case video >= 2200:
		height = 1080
	case video >= 1000:
		height = 720
	case video >= 450:
		height = 480
	}
	return compressionPlan{VideoKbps: video, AudioKbps: audio, MaxHeight: height}, true
}

func (p compressionPlan) ffmpegArgs(in, out string) []string {
	v := strconv.Itoa(p.VideoKbps) + "k"
	return []string{
		"-hide_banner", "-loglevel", "error",
		"-i", in,
		// Never upscale; keep width even for libx264.
		"-vf", fmt.Sprintf("scale=-2:'min(%d,ih)'", p.MaxHeight),
		"-c:v", "libx264", "-preset", "veryfast",
		"-b:v", v, "-maxrate", v, "-bufsize", strconv.Itoa(p.VideoKbps*2) + "k",
		"-c:a", "aac", "-b:a", strconv.Itoa(p.AudioKbps) + "k",
		"-movflags", "+faststart",
		"-y", out,
	}
}

func (y YTDLP) ffmpegBin() string {
	if y.FFmpegBin != "" {
		return y.FFmpegBin
	}
	return "ffmpeg"
}

func (y YTDLP) ffprobeBin() string {
	if y.FFprobeBin != "" {
		return y.FFprobeBin
	}
	return "ffprobe"
}

// probeDuration asks ffprobe for the container duration in seconds.
func (y YTDLP) probeDuration(ctx context.Context, path string) (float64, error) {
	out, err := exec.CommandContext(ctx, y.ffprobeBin(), "-v", "error", "-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1", path).Output()
	if err != nil {
		return 0, err
	}
	return strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
}

// compressToFit re-encodes a video that exceeds the upload limit so that it
// fits. It returns the path to send: the compressed file on success, or the
// original if it already fits or cannot be made to fit (the caller's size
// check then reports the problem to the user).
func (y YTDLP) compressToFit(ctx context.Context, log *slog.Logger, path string, durationSec float64) string {
	info, err := os.Stat(path)
	if err != nil {
		return path
	}
	limit := y.maxBytes()
	if info.Size() <= limit || !isVideoFile(path) {
		return path
	}
	if durationSec <= 0 {
		if d, err := y.probeDuration(ctx, path); err == nil {
			durationSec = d
		}
	}

	out := strings.TrimSuffix(path, filepath.Ext(path)) + "_tg.mp4"
	// If rate control overshoots, try once more with a 20% smaller budget.
	for _, scale := range []float64{1.0, 0.8} {
		plan, ok := planCompression(limit, durationSec, scale)
		if !ok {
			log.Warn("video too long to fit size limit", "duration_sec", durationSec, "limit_bytes", limit)
			return path
		}
		log.Info("compressing video for telegram", "original_bytes", info.Size(), "limit_bytes", limit,
			"video_kbps", plan.VideoKbps, "audio_kbps", plan.AudioKbps, "max_height", plan.MaxHeight)
		cmdOut, err := exec.CommandContext(ctx, y.ffmpegBin(), plan.ffmpegArgs(path, out)...).CombinedOutput()
		if err != nil {
			log.Error("ffmpeg compression failed", "error", err, "output", truncateOutput(string(cmdOut)))
			_ = os.Remove(out)
			return path
		}
		ci, err := os.Stat(out)
		if err != nil {
			return path
		}
		log.Info("compression complete", "original_bytes", info.Size(), "compressed_bytes", ci.Size())
		if ci.Size() <= limit {
			_ = os.Remove(path)
			return out
		}
	}
	_ = os.Remove(out)
	return path
}

func truncateOutput(s string) string {
	if len(s) > 2000 {
		return s[len(s)-2000:]
	}
	return s
}

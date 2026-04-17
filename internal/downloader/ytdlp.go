package downloader

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type YTDLP struct {
	Bin           string
	CookiesFile   string
	MaxFileSizeMB int64
	Logger        *slog.Logger
}

type MediaType int

const (
	MediaDocument MediaType = iota
	MediaVideo
	MediaAudio
	MediaPhoto
)

type Result struct {
	FilePath  string
	FileName  string
	Platform  string
	Media     MediaType
	Title     string
	Duration  int
}

type mediaInfo struct {
	Extractor    string  `json:"extractor_key"`
	ThumbnailURL string  `json:"thumbnail"`
	Title        string  `json:"title"`
	ID           string  `json:"id"`
	Duration     float64 `json:"duration"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	Platform     string  `json:"-"`
}

var progressRE = regexp.MustCompile(`\[download\]\s+([0-9.]+)%`)

func (y YTDLP) Download(ctx context.Context, workDir string, jobID int64, url string, onProgress func(text string, percent int)) (*Result, error) {
	logger := y.Logger
	if logger == nil {
		logger = slog.Default()
	}
	log := logger.With("component", "downloader", "job_id", jobID, "url", url, "bin", y.Bin)

	jobDir := filepath.Join(workDir, fmt.Sprintf("job-%d", jobID))
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		log.Error("create job dir failed", "job_dir", jobDir, "error", err)
		return nil, err
	}

	// Fetch metadata
	meta := y.fetchMetadata(ctx, log, url)

	// Try video download first
	result, err := y.downloadVideo(ctx, log, jobDir, url, meta, onProgress)
	if err == nil {
		return result, nil
	}

	// If video download failed, try image fallback
	errStr := err.Error()
	if strings.Contains(errStr, "No video could be found") ||
		strings.Contains(errStr, "Unsupported URL") ||
		strings.Contains(errStr, "no suitable InfoExtractor") {
		log.Info("no video found, attempting image fallback")
		if onProgress != nil {
			onProgress("No video found, trying image download", 10)
		}
		return y.downloadImage(ctx, log, jobDir, url, meta, onProgress)
	}

	return nil, err
}

// qualityFormat returns the yt-dlp format string for a given quality setting.
func qualityFormat(quality string) string {
	switch quality {
	case "q360":
		return "bestvideo[height<=360][ext=mp4]+bestaudio[ext=m4a]/best[height<=360][ext=mp4]/best[height<=360]/best"
	case "q480":
		return "bestvideo[height<=480][ext=mp4]+bestaudio[ext=m4a]/best[height<=480][ext=mp4]/best[height<=480]/best"
	case "q720":
		return "bestvideo[height<=720][ext=mp4]+bestaudio[ext=m4a]/best[height<=720][ext=mp4]/best[height<=720]/best"
	case "q1080":
		return "bestvideo[height<=1080][ext=mp4]+bestaudio[ext=m4a]/best[height<=1080][ext=mp4]/best[height<=1080]/best"
	default:
		return "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best"
	}
}

// QualityFallbackChain returns the ordered list of qualities to try,
// starting from the given quality and stepping down.
func QualityFallbackChain(quality string) []string {
	all := []string{"q1080", "q720", "q480", "q360"}
	for i, q := range all {
		if q == quality {
			return append(all[i:], "best")
		}
	}
	return []string{"best"}
}

// DownloadWithQuality downloads video at a specific quality cap.
func (y YTDLP) DownloadWithQuality(ctx context.Context, workDir string, jobID int64, url, quality string, onProgress func(text string, percent int)) (*Result, error) {
	logger := y.Logger
	if logger == nil {
		logger = slog.Default()
	}
	log := logger.With("component", "downloader", "job_id", jobID, "url", url, "quality", quality)

	jobDir := filepath.Join(workDir, fmt.Sprintf("job-%d", jobID))
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return nil, err
	}

	meta := y.fetchMetadata(ctx, log, url)

	outputTemplate := filepath.Join(jobDir, "%(title).120B [%(id)s].%(ext)s")
	args := []string{
		"--newline",
		"--no-playlist",
		"-f", qualityFormat(quality),
		"--merge-output-format", "mp4",
		"-o", outputTemplate,
	}
	if y.MaxFileSizeMB > 0 {
		args = append(args, "--max-filesize", fmt.Sprintf("%dM", y.MaxFileSizeMB))
	}
	if y.CookiesFile != "" {
		if _, err := os.Stat(y.CookiesFile); err == nil {
			args = append(args, "--cookies", y.CookiesFile)
		}
	}
	args = append(args, url)

	cmd := exec.CommandContext(ctx, y.Bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	log.Info("starting yt-dlp with quality", "format", qualityFormat(quality))
	if onProgress != nil {
		onProgress("Starting download ("+quality+")", 5)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	stdoutCh := make(chan []string, 1)
	stderrCh := make(chan []string, 1)
	go func() { stdoutCh <- readPipe(bufio.NewScanner(stdout), onProgress) }()
	go func() { stderrCh <- readPipe(bufio.NewScanner(stderr), onProgress) }()

	err = cmd.Wait()
	stdoutLines := <-stdoutCh
	stderrLines := <-stderrCh
	combined := append(stdoutLines, stderrLines...)
	if err != nil {
		out := strings.TrimSpace(strings.Join(combined, "\n"))
		return nil, fmt.Errorf("yt-dlp failed: %s", out)
	}
	if onProgress != nil {
		onProgress("Finalizing file", 98)
	}

	result, findErr := findOutputFile(jobDir, meta.Platform)
	if findErr != nil {
		return nil, findErr
	}
	result.Title = meta.Title
	result.Duration = int(meta.Duration)
	if result.Media == MediaDocument && isVideoFile(result.FileName) {
		result.Media = MediaVideo
	}
	return result, nil
}

// DownloadAudio extracts audio only and converts to mp3.
func (y YTDLP) DownloadAudio(ctx context.Context, workDir string, jobID int64, url string, onProgress func(text string, percent int)) (*Result, error) {
	logger := y.Logger
	if logger == nil {
		logger = slog.Default()
	}
	log := logger.With("component", "downloader", "job_id", jobID, "url", url, "bin", y.Bin, "mode", "audio")

	jobDir := filepath.Join(workDir, fmt.Sprintf("job-%d", jobID))
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return nil, err
	}

	meta := y.fetchMetadata(ctx, log, url)

	outputTemplate := filepath.Join(jobDir, "%(title).120B [%(id)s].%(ext)s")
	args := []string{
		"--newline",
		"--no-playlist",
		"-x",
		"--audio-format", "mp3",
		"--audio-quality", "0",
		"-o", outputTemplate,
	}
	if y.MaxFileSizeMB > 0 {
		args = append(args, "--max-filesize", fmt.Sprintf("%dM", y.MaxFileSizeMB))
	}
	if y.CookiesFile != "" {
		if _, err := os.Stat(y.CookiesFile); err == nil {
			args = append(args, "--cookies", y.CookiesFile)
		}
	}
	args = append(args, url)

	cmd := exec.CommandContext(ctx, y.Bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	log.Info("starting yt-dlp audio extraction")
	if onProgress != nil {
		onProgress("Extracting audio", 5)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	stdoutCh := make(chan []string, 1)
	stderrCh := make(chan []string, 1)
	go func() { stdoutCh <- readPipe(bufio.NewScanner(stdout), onProgress) }()
	go func() { stderrCh <- readPipe(bufio.NewScanner(stderr), onProgress) }()

	err = cmd.Wait()
	stdoutLines := <-stdoutCh
	stderrLines := <-stderrCh
	combined := append(stdoutLines, stderrLines...)
	if err != nil {
		out := strings.TrimSpace(strings.Join(combined, "\n"))
		log.Error("yt-dlp audio extraction failed", "output", out, "error", err)
		return nil, fmt.Errorf("audio extraction failed: %s", out)
	}

	result, findErr := findOutputFile(jobDir, meta.Platform)
	if findErr != nil {
		return nil, findErr
	}
	result.Media = MediaAudio
	result.Title = meta.Title
	result.Duration = int(meta.Duration)
	return result, nil
}

func (y YTDLP) fetchMetadata(ctx context.Context, log *slog.Logger, url string) mediaInfo {
	args := []string{"--dump-single-json", "--no-playlist", "--no-download"}
	if y.CookiesFile != "" {
		if _, err := os.Stat(y.CookiesFile); err == nil {
			args = append(args, "--cookies", y.CookiesFile)
		}
	}
	args = append(args, url)

	cmd := exec.CommandContext(ctx, y.Bin, args...)
	out, err := cmd.Output()
	meta := mediaInfo{Platform: "unknown"}
	if err == nil {
		if json.Unmarshal(out, &meta) == nil && meta.Extractor != "" {
			meta.Platform = strings.ToLower(meta.Extractor)
		}
		log.Info("metadata fetched", "platform", meta.Platform)
	} else {
		log.Warn("metadata fetch failed", "error", err)
	}
	return meta
}

func (y YTDLP) downloadVideo(ctx context.Context, log *slog.Logger, jobDir, url string, meta mediaInfo, onProgress func(text string, percent int)) (*Result, error) {
	outputTemplate := filepath.Join(jobDir, "%(title).120B [%(id)s].%(ext)s")
	args := []string{
		"--newline",
		"--no-playlist",
		"-f", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
		"--merge-output-format", "mp4",
		"-o", outputTemplate,
	}
	if y.MaxFileSizeMB > 0 {
		args = append(args, "--max-filesize", fmt.Sprintf("%dM", y.MaxFileSizeMB))
	}
	if y.CookiesFile != "" {
		if _, err := os.Stat(y.CookiesFile); err == nil {
			args = append(args, "--cookies", y.CookiesFile)
		}
	}
	args = append(args, url)

	cmd := exec.CommandContext(ctx, y.Bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}

	log.Info("starting yt-dlp", "output_template", outputTemplate)
	if onProgress != nil {
		onProgress("Starting downloader", 5)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	stdoutCh := make(chan []string, 1)
	stderrCh := make(chan []string, 1)
	go func() { stdoutCh <- readPipe(bufio.NewScanner(stdout), onProgress) }()
	go func() { stderrCh <- readPipe(bufio.NewScanner(stderr), onProgress) }()

	err = cmd.Wait()
	stdoutLines := <-stdoutCh
	stderrLines := <-stderrCh
	combined := append(stdoutLines, stderrLines...)
	if err != nil {
		out := strings.TrimSpace(strings.Join(combined, "\n"))
		log.Error("yt-dlp failed", "output", out, "error", err)
		return nil, fmt.Errorf("yt-dlp failed: %s", out)
	}
	if onProgress != nil {
		onProgress("Finalizing file", 98)
	}
	log.Info("yt-dlp finished")

	result, findErr := findOutputFile(jobDir, meta.Platform)
	if findErr != nil {
		return nil, findErr
	}
	result.Title = meta.Title
	result.Duration = int(meta.Duration)
	if result.Media == MediaDocument && isVideoFile(result.FileName) {
		result.Media = MediaVideo
	}
	return result, nil
}

func (y YTDLP) downloadImage(ctx context.Context, log *slog.Logger, jobDir, url string, meta mediaInfo, onProgress func(text string, percent int)) (*Result, error) {
	// Try yt-dlp with --write-thumbnail --skip-download first
	outputTemplate := filepath.Join(jobDir, "%(title).120B [%(id)s].%(ext)s")
	args := []string{
		"--no-playlist",
		"--write-thumbnail",
		"--skip-download",
		"--convert-thumbnails", "jpg",
		"-o", outputTemplate,
	}
	if y.CookiesFile != "" {
		if _, err := os.Stat(y.CookiesFile); err == nil {
			args = append(args, "--cookies", y.CookiesFile)
		}
	}
	args = append(args, url)

	cmd := exec.CommandContext(ctx, y.Bin, args...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		log.Info("thumbnail download succeeded")
		if onProgress != nil {
			onProgress("Image downloaded", 95)
		}
		result, findErr := findOutputFile(jobDir, meta.Platform)
		if findErr == nil {
			result.Media = MediaPhoto
			result.Title = meta.Title
			return result, nil
		}
		log.Warn("thumbnail file not found after download", "error", findErr)
	} else {
		log.Warn("thumbnail download failed", "output", string(out), "error", err)
	}

	// Last resort: download the thumbnail URL directly
	if meta.ThumbnailURL != "" {
		log.Info("downloading thumbnail URL directly", "url", meta.ThumbnailURL)
		if onProgress != nil {
			onProgress("Downloading image", 50)
		}
		return downloadURL(ctx, jobDir, meta.ThumbnailURL, meta.ID, meta.Platform)
	}

	return nil, fmt.Errorf("no video or image could be extracted from this URL")
}

func downloadURL(ctx context.Context, jobDir, url, id, platform string) (*Result, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("image download failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image download returned status %d", resp.StatusCode)
	}

	ext := "jpg"
	ct := resp.Header.Get("Content-Type")
	switch {
	case strings.Contains(ct, "png"):
		ext = "png"
	case strings.Contains(ct, "webp"):
		ext = "webp"
	case strings.Contains(ct, "gif"):
		ext = "gif"
	}

	fileName := fmt.Sprintf("%s.%s", id, ext)
	filePath := filepath.Join(jobDir, fileName)
	f, err := os.Create(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return nil, err
	}

	return &Result{
		FilePath: filePath,
		FileName: fileName,
		Platform: platform,
		Media:    MediaPhoto,
	}, nil
}

func findOutputFile(jobDir, platform string) (*Result, error) {
	entries, err := os.ReadDir(jobDir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".ytdl") {
			continue
		}
		media := detectMediaType(name)
		return &Result{
			FilePath: filepath.Join(jobDir, name),
			FileName: name,
			Platform: platform,
			Media:    media,
		}, nil
	}
	return nil, fmt.Errorf("no output file found in %s", jobDir)
}

func detectMediaType(name string) MediaType {
	if isImageFile(name) {
		return MediaPhoto
	}
	if isVideoFile(name) {
		return MediaVideo
	}
	if isAudioFile(name) {
		return MediaAudio
	}
	return MediaDocument
}

func isVideoFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".mp4") ||
		strings.HasSuffix(lower, ".mkv") ||
		strings.HasSuffix(lower, ".webm") ||
		strings.HasSuffix(lower, ".mov") ||
		strings.HasSuffix(lower, ".avi")
}

func isAudioFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".mp3") ||
		strings.HasSuffix(lower, ".m4a") ||
		strings.HasSuffix(lower, ".ogg") ||
		strings.HasSuffix(lower, ".opus") ||
		strings.HasSuffix(lower, ".flac") ||
		strings.HasSuffix(lower, ".wav")
}

func isImageFile(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".jpg") ||
		strings.HasSuffix(lower, ".jpeg") ||
		strings.HasSuffix(lower, ".png") ||
		strings.HasSuffix(lower, ".webp") ||
		strings.HasSuffix(lower, ".gif")
}


// PlaylistEntry represents a single video in a playlist.
type PlaylistEntry struct {
	Title string
	URL   string
}

type playlistJSON struct {
	Title   string `json:"title"`
	Entries []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		ID    string `json:"id"`
	} `json:"entries"`
}

// FetchPlaylist fetches playlist metadata and returns individual entry URLs.
func (y YTDLP) FetchPlaylist(ctx context.Context, url string, maxItems int) ([]PlaylistEntry, string, error) {
	args := []string{
		"--dump-single-json",
		"--flat-playlist",
		"--no-download",
		"--playlist-end", strconv.Itoa(maxItems),
	}
	if y.CookiesFile != "" {
		if _, err := os.Stat(y.CookiesFile); err == nil {
			args = append(args, "--cookies", y.CookiesFile)
		}
	}
	args = append(args, url)

	cmd := exec.CommandContext(ctx, y.Bin, args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, "", fmt.Errorf("playlist fetch failed: %w", err)
	}

	var pl playlistJSON
	if err := json.Unmarshal(out, &pl); err != nil {
		return nil, "", fmt.Errorf("parse playlist JSON: %w", err)
	}

	var entries []PlaylistEntry
	for _, e := range pl.Entries {
		entryURL := e.URL
		if entryURL == "" && e.ID != "" {
			entryURL = "https://www.youtube.com/watch?v=" + e.ID
		}
		if entryURL == "" {
			continue
		}
		title := e.Title
		if title == "" {
			title = "(untitled)"
		}
		entries = append(entries, PlaylistEntry{Title: title, URL: entryURL})
	}
	return entries, pl.Title, nil
}

func readPipe(scanner *bufio.Scanner, onProgress func(text string, percent int)) []string {
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)
		if m := progressRE.FindStringSubmatch(line); len(m) == 2 && onProgress != nil {
			pct, _ := strconv.ParseFloat(m[1], 64)
			percent := int(pct)
			if percent < 5 {
				percent = 5
			}
			if percent > 95 {
				percent = 95
			}
			onProgress(fmt.Sprintf("Downloading %s", m[1]+"%"), percent)
		}
	}
	return lines
}

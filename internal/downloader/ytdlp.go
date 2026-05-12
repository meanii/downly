package downloader

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
	Extractor       string  `json:"extractor_key"`
	ThumbnailURL    string  `json:"thumbnail"`
	Title           string  `json:"title"`
	ID              string  `json:"id"`
	Duration        float64 `json:"duration"`
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	Filesize        int64   `json:"filesize"`
	FilesizeApprox  int64   `json:"filesize_approx"`
	Platform        string  `json:"-"`
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

	// Direct image URL — skip yt-dlp entirely.
	if isImageURL(url) {
		log.Info("detected direct image URL, downloading directly")
		if onProgress != nil {
			onProgress("Downloading image", 10)
		}
		return downloadURL(ctx, jobDir, url, "image", "direct")
	}

	// Fetch metadata
	meta := y.fetchMetadata(ctx, log, url)

	if err := y.checkSize(meta); err != nil {
		return nil, err
	}

	// Try video-specific download first.
	result, err := y.downloadVideo(ctx, log, jobDir, url, meta, onProgress)
	if err == nil {
		return result, nil
	}

	// On any yt-dlp failure, retry without a format selector — this handles
	// photo posts (Instagram, Twitter/X, Reddit, etc.) where yt-dlp can
	// download the image but rejects the video format string.
	errStr := err.Error()
	if strings.Contains(errStr, "yt-dlp failed") {
		log.Info("video download failed, retrying without format selector", "error", err)
		if onProgress != nil {
			onProgress("Trying alternate download", 5)
		}
		result, err2 := y.downloadAny(ctx, log, jobDir, url, meta, onProgress)
		if err2 == nil {
			return result, nil
		}
		log.Info("generic download also failed", "error", err2)
		err = err2
		errStr = err.Error()
	}

	// Final fallback: extract thumbnail / fetch image directly.
	if strings.Contains(errStr, "No video could be found") ||
		strings.Contains(errStr, "Unsupported URL") ||
		strings.Contains(errStr, "no suitable InfoExtractor") ||
		strings.Contains(errStr, "is not a valid URL") ||
		strings.Contains(errStr, "Unable to download") ||
		strings.Contains(errStr, "yt-dlp failed") {
		log.Info("no media found via yt-dlp, attempting image fallback")
		if onProgress != nil {
			onProgress("Trying image download", 10)
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

	if err := y.checkSize(meta); err != nil {
		return nil, err
	}

	outputTemplate := filepath.Join(jobDir, "%(id)s.%(ext)s")
	args := []string{
		"--ignore-config",
		"--newline",
		"--no-playlist",
		"-f", qualityFormat(quality),
		"--merge-output-format", "mp4",
		"-o", outputTemplate,
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
	result.FileName = friendlyFileName(meta.Title, meta.ID, result.FileName)
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

	outputTemplate := filepath.Join(jobDir, "%(id)s.%(ext)s")
	args := []string{
		"--ignore-config",
		"--newline",
		"--no-playlist",
		"-x",
		"--audio-format", "mp3",
		"--audio-quality", "0",
		"-o", outputTemplate,
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
	result.FileName = friendlyFileName(meta.Title, meta.ID, result.FileName)
	return result, nil
}

// checkSize returns an error when the known/approximate file size from metadata
// already exceeds MaxFileSizeMB, so we don't waste bandwidth downloading it.
func (y YTDLP) checkSize(meta mediaInfo) error {
	if y.MaxFileSizeMB <= 0 {
		return nil
	}
	limit := y.MaxFileSizeMB * 1024 * 1024
	size := meta.Filesize
	if size == 0 {
		size = meta.FilesizeApprox
	}
	if size > 0 && size > limit {
		return fmt.Errorf("video is ~%.0fMB, exceeds the %dMB limit — try a lower quality (720p, 480p)", float64(size)/1024/1024, y.MaxFileSizeMB)
	}
	return nil
}

func (y YTDLP) fetchMetadata(ctx context.Context, log *slog.Logger, url string) mediaInfo {
	args := []string{"--ignore-config", "--dump-single-json", "--no-playlist", "--no-download"}
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
	outputTemplate := filepath.Join(jobDir, "%(id)s.%(ext)s")
	args := []string{
		"--ignore-config",
		"--newline",
		"--no-playlist",
		"-f", "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best",
		"--merge-output-format", "mp4",
		"-o", outputTemplate,
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
	result.FileName = friendlyFileName(meta.Title, meta.ID, result.FileName)
	if result.Media == MediaDocument && isVideoFile(result.FileName) {
		result.Media = MediaVideo
	}
	return result, nil
}

// downloadAny runs yt-dlp without a format selector, letting it pick whatever
// is available — images, mixed media, or video formats yt-dlp handles natively.
func (y YTDLP) downloadAny(ctx context.Context, log *slog.Logger, jobDir, url string, meta mediaInfo, onProgress func(text string, percent int)) (*Result, error) {
	outputTemplate := filepath.Join(jobDir, "%(id)s.%(ext)s")
	args := []string{
		"--ignore-config",
		"--newline",
		"--no-playlist",
		"-o", outputTemplate,
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

	if onProgress != nil {
		onProgress("Downloading", 5)
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	stdoutCh := make(chan []string, 1)
	stderrCh := make(chan []string, 1)
	go func() { stdoutCh <- readPipe(bufio.NewScanner(stdout), onProgress) }()
	go func() { stderrCh <- readPipe(bufio.NewScanner(stderr), nil) }()

	cmdErr := cmd.Wait()
	stdoutLines := <-stdoutCh
	stderrLines := <-stderrCh
	combined := append(stdoutLines, stderrLines...)
	if cmdErr != nil {
		out := strings.TrimSpace(strings.Join(combined, "\n"))
		return nil, fmt.Errorf("yt-dlp failed: %s", out)
	}
	if onProgress != nil {
		onProgress("Finalizing", 98)
	}

	result, findErr := findOutputFile(jobDir, meta.Platform)
	if findErr != nil {
		return nil, findErr
	}
	result.Title = meta.Title
	result.Duration = int(meta.Duration)
	result.FileName = friendlyFileName(meta.Title, meta.ID, result.FileName)
	return result, nil
}

func (y YTDLP) downloadImage(ctx context.Context, log *slog.Logger, jobDir, url string, meta mediaInfo, onProgress func(text string, percent int)) (*Result, error) {
	// Try yt-dlp with --write-thumbnail --skip-download first
	outputTemplate := filepath.Join(jobDir, "%(id)s.%(ext)s")
	args := []string{
		"--ignore-config",
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

	// Try downloading the thumbnail URL directly.
	if meta.ThumbnailURL != "" {
		log.Info("downloading thumbnail URL directly", "url", meta.ThumbnailURL)
		if onProgress != nil {
			onProgress("Downloading image", 50)
		}
		return downloadURL(ctx, jobDir, meta.ThumbnailURL, meta.ID, meta.Platform)
	}

	// Last resort: fetch the URL directly if it looks like an image.
	if isImageURL(url) {
		log.Info("fetching URL directly as image")
		if onProgress != nil {
			onProgress("Downloading image directly", 50)
		}
		return downloadURL(ctx, jobDir, url, "image", "direct")
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

// friendlyFileName builds a human-readable filename from title + id metadata,
// preserving the extension from the actual downloaded file.
func friendlyFileName(title, id, diskName string) string {
	ext := filepath.Ext(diskName)
	if title == "" {
		if id != "" {
			return id + ext
		}
		return diskName
	}
	// Sanitize: replace path separators and null bytes.
	safe := strings.NewReplacer("/", "_", "\\", "_", "\x00", "").Replace(title)
	if len(safe) > 120 {
		safe = safe[:120]
	}
	if id != "" {
		return safe + " [" + id + "]" + ext
	}
	return safe + ext
}

var skipExts = map[string]bool{
	".part": true, ".ytdl": true, ".json": true, ".tmp": true,
}

func findOutputFile(jobDir, platform string) (*Result, error) {
	var found string
	err := filepath.WalkDir(jobDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || found != "" {
			return nil
		}
		name := d.Name()
		if skipExts[strings.ToLower(filepath.Ext(name))] {
			return nil
		}
		found = path
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found == "" {
		// Log directory contents to help diagnose why yt-dlp wrote nothing.
		if entries, readErr := os.ReadDir(jobDir); readErr == nil {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Name())
			}
			slog.Default().Warn("no usable file found after yt-dlp exit 0", "job_dir", jobDir, "dir_contents", names)
		} else {
			slog.Default().Warn("no usable file found and job dir unreadable", "job_dir", jobDir, "error", readErr)
		}
		return nil, fmt.Errorf("no output file found in %s", jobDir)
	}
	name := filepath.Base(found)
	return &Result{
		FilePath: found,
		FileName: name,
		Platform: platform,
		Media:    detectMediaType(name),
	}, nil
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

// isImageURL returns true when the URL path ends with an image extension,
// indicating a direct image link that can be fetched without yt-dlp.
func isImageURL(rawURL string) bool {
	// Strip query string before checking extension.
	path := rawURL
	if i := strings.Index(path, "?"); i >= 0 {
		path = path[:i]
	}
	return isImageFile(path)
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
	// phase tracks how many download streams have started (for video+audio 2-stream downloads).
	// Phase 1 maps to 5–48 %, phase 2 maps to 48–95 %, single-stream maps to 5–95 %.
	phase := 0
	lastMapped := 0
	for scanner.Scan() {
		line := scanner.Text()
		lines = append(lines, line)

		if strings.Contains(line, "[download] Destination:") {
			phase++
			continue
		}

		if m := progressRE.FindStringSubmatch(line); len(m) == 2 && onProgress != nil {
			pct, _ := strconv.ParseFloat(m[1], 64)
			p := int(pct)

			var mapped int
			switch phase {
			case 1:
				mapped = 5 + p*43/100
			case 2:
				mapped = 48 + p*47/100
			default:
				mapped = 5 + p*90/100
			}
			if mapped < 5 {
				mapped = 5
			}
			if mapped > 95 {
				mapped = 95
			}
			if mapped < lastMapped {
				mapped = lastMapped
			}
			lastMapped = mapped
			onProgress(fmt.Sprintf("Downloading %s%%", m[1]), mapped)
		}
	}
	return lines
}

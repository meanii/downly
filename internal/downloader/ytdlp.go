package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type YTDLP struct {
	Bin string
}

type Result struct {
	FilePath string
	FileName string
	Platform string
}

type info struct {
	Extractor string `json:"extractor_key"`
}

func (y YTDLP) Download(ctx context.Context, workDir string, jobID int64, url string) (*Result, error) {
	jobDir := filepath.Join(workDir, fmt.Sprintf("job-%d", jobID))
	if err := os.MkdirAll(jobDir, 0o755); err != nil {
		return nil, err
	}

	platform := "unknown"
	infoCmd := exec.CommandContext(ctx, y.Bin, "--dump-single-json", "--no-playlist", url)
	infoOut, err := infoCmd.Output()
	if err == nil {
		var meta info
		if json.Unmarshal(infoOut, &meta) == nil && meta.Extractor != "" {
			platform = strings.ToLower(meta.Extractor)
		}
	}

	outputTemplate := filepath.Join(jobDir, "%(title).120B [%(id)s].%(ext)s")
	cmd := exec.CommandContext(ctx, y.Bin, "--no-playlist", "-o", outputTemplate, url)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("yt-dlp failed: %s", strings.TrimSpace(string(combined)))
	}

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
		return &Result{
			FilePath: filepath.Join(jobDir, name),
			FileName: name,
			Platform: platform,
		}, nil
	}

	return nil, fmt.Errorf("download completed but no output file found")
}

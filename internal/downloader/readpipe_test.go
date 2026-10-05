package downloader

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadPipeDrainsOverlongLines(t *testing.T) {
	// A single 3 MiB line exceeds the scanner buffer. readPipe must still
	// consume everything so the writer (yt-dlp) never blocks.
	pr, pw := io.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = pw.Write([]byte(strings.Repeat("x", 3*1024*1024) + "\n"))
		_, _ = pw.Write([]byte("after\n"))
		_ = pw.Close()
	}()
	go readPipe(pr, nil)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("writer blocked: readPipe stopped draining the pipe")
	}
}

func TestReadPipeKeepsTailAndSkipsProgress(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
		fmt.Fprintf(&b, "[download]  %d.0%% of 10MiB\n", i)
	}
	var calls int
	lines := readPipe(strings.NewReader(b.String()), func(string, int) { calls++ })
	if len(lines) != maxKeptLines {
		t.Fatalf("kept %d lines, want %d", len(lines), maxKeptLines)
	}
	if lines[len(lines)-1] != "line 99" {
		t.Fatalf("last kept line = %q", lines[len(lines)-1])
	}
	for _, l := range lines {
		if strings.HasPrefix(l, "[download]") {
			t.Fatalf("progress line kept: %q", l)
		}
	}
	if calls != 100 {
		t.Fatalf("progress callback called %d times, want 100", calls)
	}
}

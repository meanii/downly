package downloader

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsUnavailable(t *testing.T) {
	yes := []error{
		errors.New("yt-dlp failed: ERROR: [youtube] x: Private video. Sign in if you've been granted access"),
		errors.New("ERROR: [youtube] x: Video unavailable. This video has been removed by the uploader"),
		errors.New("ERROR: Join this channel to get access to members-only content"),
		fmt.Errorf("wrapped: %w", ErrTooLarge),
	}
	no := []error{
		nil,
		errors.New("yt-dlp failed: ERROR: [instagram] x: There is no video in this post"),
		errors.New("ERROR: Unsupported URL: https://example.com/a.jpg"),
		errors.New("HTTP Error 503: Service Unavailable"),
	}
	for _, e := range yes {
		if !IsUnavailable(e) {
			t.Errorf("should be unavailable: %v", e)
		}
	}
	for _, e := range no {
		if IsUnavailable(e) {
			t.Errorf("should allow fallback/retry: %v", e)
		}
	}
}

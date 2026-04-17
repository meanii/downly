package downloader

import (
	"testing"
)

func TestDetectMediaType(t *testing.T) {
	tests := []struct {
		name     string
		expected MediaType
	}{
		{"video.mp4", MediaVideo},
		{"video.MP4", MediaVideo},
		{"video.mkv", MediaVideo},
		{"video.webm", MediaVideo},
		{"video.mov", MediaVideo},
		{"photo.jpg", MediaPhoto},
		{"photo.JPEG", MediaPhoto},
		{"photo.png", MediaPhoto},
		{"photo.webp", MediaPhoto},
		{"photo.gif", MediaPhoto},
		{"audio.mp3", MediaAudio},
		{"audio.m4a", MediaAudio},
		{"audio.ogg", MediaAudio},
		{"audio.opus", MediaAudio},
		{"audio.flac", MediaAudio},
		{"audio.wav", MediaAudio},
		{"file.zip", MediaDocument},
		{"file.pdf", MediaDocument},
		{"file.txt", MediaDocument},
		{"noext", MediaDocument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectMediaType(tt.name)
			if got != tt.expected {
				t.Errorf("detectMediaType(%q) = %d, want %d", tt.name, got, tt.expected)
			}
		})
	}
}

func TestIsImageFile(t *testing.T) {
	if !isImageFile("test.jpg") {
		t.Error("expected jpg to be image")
	}
	if !isImageFile("test.PNG") {
		t.Error("expected PNG to be image")
	}
	if isImageFile("test.mp4") {
		t.Error("expected mp4 to not be image")
	}
}

func TestIsVideoFile(t *testing.T) {
	if !isVideoFile("test.mp4") {
		t.Error("expected mp4 to be video")
	}
	if !isVideoFile("test.MKV") {
		t.Error("expected MKV to be video")
	}
	if isVideoFile("test.jpg") {
		t.Error("expected jpg to not be video")
	}
}

func TestIsAudioFile(t *testing.T) {
	if !isAudioFile("test.mp3") {
		t.Error("expected mp3 to be audio")
	}
	if !isAudioFile("test.FLAC") {
		t.Error("expected FLAC to be audio")
	}
	if isAudioFile("test.mp4") {
		t.Error("expected mp4 to not be audio")
	}
}

func TestProgressRE(t *testing.T) {
	tests := []struct {
		line    string
		matched bool
		percent string
	}{
		{"[download]  50.5% of 10.00MiB", true, "50.5"},
		{"[download] 100% of 10.00MiB", true, "100"},
		{"[download]   0.0% of 10.00MiB", true, "0.0"},
		{"some other line", false, ""},
		{"[info] Downloading", false, ""},
	}
	for _, tt := range tests {
		m := progressRE.FindStringSubmatch(tt.line)
		if tt.matched {
			if len(m) < 2 {
				t.Errorf("expected match for %q", tt.line)
				continue
			}
			if m[1] != tt.percent {
				t.Errorf("expected percent %q, got %q for line %q", tt.percent, m[1], tt.line)
			}
		} else {
			if len(m) >= 2 {
				t.Errorf("did not expect match for %q", tt.line)
			}
		}
	}
}

func TestQualityFormat(t *testing.T) {
	tests := []struct {
		quality  string
		contains string
	}{
		{"q360", "height<=360"},
		{"q480", "height<=480"},
		{"q720", "height<=720"},
		{"q1080", "height<=1080"},
		{"qbest", "bestvideo[ext=mp4]"},
		{"", "bestvideo[ext=mp4]"},
	}
	for _, tt := range tests {
		got := qualityFormat(tt.quality)
		found := false
		for i := 0; i <= len(got)-len(tt.contains); i++ {
			if got[i:i+len(tt.contains)] == tt.contains {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("qualityFormat(%q) = %q, expected to contain %q", tt.quality, got, tt.contains)
		}
	}
}

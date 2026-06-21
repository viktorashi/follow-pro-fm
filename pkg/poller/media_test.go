package poller

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGetAudioDuration_MatchesFFprobe(t *testing.T) {
	// Skip if ffprobe is not installed
	_, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe not installed, skipping duration validation test")
	}

	// Read all ogg files in data/audios
	audiosDir := filepath.Join("..", "..", "data", "audios")
	entries, err := os.ReadDir(audiosDir)
	if err != nil {
		t.Skipf("failed to read audios dir (might not exist in test env): %v", err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(audiosDir, e.Name())

		// Only test formats we explicitly support extracting perfectly
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".ogg") && !strings.HasSuffix(strings.ToLower(e.Name()), ".wav") {
			continue
		}

		// Test our pure Go function
		goDuration, err := GetAudioDuration(path)
		if err != nil {
			t.Errorf("GetAudioDuration failed for %s: %v", path, err)
			continue
		}

		// Ask ffprobe
		cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
		var out bytes.Buffer
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			t.Errorf("ffprobe failed for %s: %v", path, err)
			continue
		}

		ffprobeStr := strings.TrimSpace(out.String())
		ffprobeSec, err := strconv.ParseFloat(ffprobeStr, 64)
		if err != nil {
			t.Errorf("failed to parse ffprobe output %q for %s", ffprobeStr, path)
			continue
		}
		ffprobeDuration := time.Duration(ffprobeSec * float64(time.Second))

		// Compare them. Acceptable delta: 0.1s
		delta := goDuration - ffprobeDuration
		if delta < 0 {
			delta = -delta
		}

		if delta > 100*time.Millisecond {
			t.Errorf("Duration mismatch for %s: Go=%v, ffprobe=%v (delta=%v)", e.Name(), goDuration, ffprobeDuration, delta)
		} else {
			t.Logf("Match %s: Go=%v, ffprobe=%v", e.Name(), goDuration, ffprobeDuration)
		}
	}
}

func TestExtractWaveform(t *testing.T) {
	// Skip if ffmpeg is not installed
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed, skipping waveform extraction test")
	}

	// Read all ogg files in data/audios
	audiosDir := filepath.Join("..", "..", "data", "audios")
	entries, err := os.ReadDir(audiosDir)
	if err != nil {
		t.Skipf("failed to read audios dir (might not exist in test env): %v", err)
	}

	var testedAny bool
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".ogg") {
			continue
		}
		path := filepath.Join(audiosDir, e.Name())

		waveform, err := ExtractWaveform(path)
		if err != nil {
			t.Errorf("ExtractWaveform failed for %s: %v", path, err)
			continue
		}

		testedAny = true

		if len(waveform) != 64 {
			t.Errorf("Waveform length is %d, expected 64 for %s", len(waveform), path)
		}

		// Find peak value
		maxVal := byte(0)
		for _, v := range waveform {
			if v > maxVal {
				maxVal = v
			}
		}

		if maxVal != 255 {
			t.Errorf("Peak value is %d, expected 255 for %s", maxVal, path)
		}
	}

	if !testedAny {
		t.Errorf("Did not find any .ogg files to test in %s", audiosDir)
	}

	// Test graceful error handling
	badPath := "non_existent_file.ogg"
	waveform, err := ExtractWaveform(badPath)
	if err == nil {
		t.Error("Expected error for non-existent file, but got nil")
	}
	if len(waveform) != 64 {
		t.Errorf("Expected 64-byte waveform on error, got length %d", len(waveform))
	}
	// All bytes should be 0
	for idx, val := range waveform {
		if val != 0 {
			t.Errorf("Expected zero byte at index %d, got %d", idx, val)
		}
	}
}

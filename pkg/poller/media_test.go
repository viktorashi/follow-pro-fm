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
	ffprobePath, err := ffprobeBinaryPath()
	if err != nil {
		t.Skip("ffprobe not installed, skipping duration validation test")
	}

	path := filepath.Join("testdata", "waveform_sample.ogg")

	goDuration, err := GetAudioDuration(path)
	if err != nil {
		t.Fatalf("GetAudioDuration failed for %s: %v", path, err)
	}

	cmd := exec.Command(ffprobePath, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("ffprobe failed for %s: %v", path, err)
	}

	ffprobeStr := strings.TrimSpace(out.String())
	ffprobeSec, err := strconv.ParseFloat(ffprobeStr, 64)
	if err != nil {
		t.Fatalf("failed to parse ffprobe output %q for %s", ffprobeStr, path)
	}
	ffprobeDuration := time.Duration(ffprobeSec * float64(time.Second))

	delta := goDuration - ffprobeDuration
	if delta < 0 {
		delta = -delta
	}

	if delta > 100*time.Millisecond {
		t.Errorf("Duration mismatch for %s: Go=%v, ffprobe=%v (delta=%v)", path, goDuration, ffprobeDuration, delta)
	} else {
		t.Logf("Match %s: Go=%v, ffprobe=%v", path, goDuration, ffprobeDuration)
	}
}

func TestExtractWaveform(t *testing.T) {
	ffmpegPath, err := ffmpegBinaryPath()
	if err != nil {
		t.Skip("ffmpeg not installed, skipping waveform extraction test")
	}

	path := filepath.Join("testdata", "waveform_sample.ogg")

	waveform, err := ExtractWaveform(path)
	if err != nil {
		t.Fatalf("ExtractWaveform failed for %s: %v", path, err)
	}

	if len(waveform) != 64 {
		t.Fatalf("waveform length is %d, expected 64 for %s", len(waveform), path)
	}

	expected := expectedWaveformSample()
	if !bytes.Equal(waveform, expected) {
		t.Fatalf("waveform mismatch for %s:\nactual:   %v\nexpected: %v", path, waveform, expected)
	}

	// Test graceful error handling
	badPath := "non_existent_file.ogg"
	waveform, err = ExtractWaveform(badPath)
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

	tmpDir := t.TempDir()
	remuxedPath := filepath.Join(tmpDir, "remuxed.ogg")
	cmd := exec.Command(ffmpegPath, "-v", "error", "-y", "-i", path, "-c", "copy", "-metadata", "creation_time=2026-06-22T10:20:30Z", remuxedPath)
	if err := cmd.Run(); err != nil {
		t.Fatalf("bundled ffmpeg failed metadata remux for %s: %v", path, err)
	}

	info, err := os.Stat(remuxedPath)
	if err != nil {
		t.Fatalf("expected remuxed file at %s: %v", remuxedPath, err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected remuxed file at %s to be non-empty", remuxedPath)
	}
}

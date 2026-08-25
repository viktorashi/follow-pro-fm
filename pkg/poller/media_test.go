//go:build e2e

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

func TestExtractWaveform_RemuxPreservesWaveform(t *testing.T) {
	ffmpegPath, err := ffmpegBinaryPath()
	if err != nil {
		t.Skip("ffmpeg not installed, skipping remux waveform validation")
	}

	originalPath := filepath.Join("testdata", "waveform_sample.ogg")
	originalWaveform, err := ExtractWaveform(originalPath)
	if err != nil {
		t.Fatalf("ExtractWaveform(original) failed: %v", err)
	}

	remuxedPath := filepath.Join(t.TempDir(), "waveform_sample.remuxed.ogg")
	cmd := exec.Command(ffmpegPath, "-v", "error", "-y", "-i", originalPath, "-c", "copy", "-metadata", "creation_time=2026-06-28T12:00:00Z", remuxedPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("bundled ffmpeg remux failed: %v (output: %s)", err, strings.TrimSpace(string(out)))
	}

	remuxedWaveform, err := ExtractWaveform(remuxedPath)
	if err != nil {
		t.Fatalf("ExtractWaveform(remuxed) failed: %v", err)
	}

	if !bytes.Equal(originalWaveform, remuxedWaveform) {
		t.Fatalf("waveform changed after remux:\noriginal: %v\nremuxed: %v", originalWaveform, remuxedWaveform)
	}
}

func TestDecodeAudioForFingerprinting_AllFixtures(t *testing.T) {
	if _, err := ffmpegBinaryPath(); err != nil {
		t.Skip("ffmpeg not installed, skipping fixture decode validation")
	}

	root := filepath.Join("testdata", "fingerprint", "cases")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", root, err)
	}

	found := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		caseDir := filepath.Join(root, entry.Name())
		audioEntries, err := os.ReadDir(caseDir)
		if err != nil {
			t.Fatalf("ReadDir(%s) error = %v", caseDir, err)
		}

		for _, audioEntry := range audioEntries {
			if audioEntry.IsDir() || strings.ToLower(filepath.Ext(audioEntry.Name())) != ".mp3" {
				continue
			}

			found++
			name := filepath.Join(entry.Name(), audioEntry.Name())
			path := filepath.Join(caseDir, audioEntry.Name())
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%s) error = %v", path, err)
			}

			t.Run(name, func(t *testing.T) {
				pcm, err := decodeAudioForFingerprinting(data, "")
				if err != nil {
					t.Fatalf("decodeAudioForFingerprinting(%s) error = %v", path, err)
				}
				if len(pcm) == 0 {
					t.Fatalf("decodeAudioForFingerprinting(%s) returned empty pcm", path)
				}
			})
		}
	}

	if found == 0 {
		t.Fatal("expected fingerprint audio fixtures")
	}
}

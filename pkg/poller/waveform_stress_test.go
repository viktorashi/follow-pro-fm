package poller

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractWaveform_Stress(t *testing.T) {
	// 1. Create a temp directory for test files
	tempDir, err := os.MkdirTemp("", "waveform_stress")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Helper function to generate an ogg file with specific audio properties using system ffmpeg
	generateOgg := func(name string, filterStr string) string {
		outPath := filepath.Join(tempDir, name)

		// Filter out "./bin" from PATH so we find the system ffmpeg with libopus encoding support
		originalPath := os.Getenv("PATH")
		pathElements := filepath.SplitList(originalPath)
		var filteredPathElements []string
		for _, pe := range pathElements {
			if pe == "./bin" || pe == "bin" || strings.HasSuffix(pe, "/pro-fm/bin") {
				continue
			}
			filteredPathElements = append(filteredPathElements, pe)
		}

		// Find system ffmpeg
		oldPathEnv := os.Getenv("PATH")
		_ = os.Setenv("PATH", strings.Join(filteredPathElements, string(filepath.ListSeparator)))
		systemFfmpeg, err := exec.LookPath("ffmpeg")
		_ = os.Setenv("PATH", oldPathEnv)

		if err != nil {
			// Fallback to homebrew path
			systemFfmpeg = "/opt/homebrew/bin/ffmpeg"
		}

		cmd := exec.Command(systemFfmpeg, "-y", "-f", "lavfi", "-i", filterStr, "-c:a", "libopus", "-b:a", "64k", "-f", "ogg", outPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to generate ogg %s using %s: %v (output: %s)", name, systemFfmpeg, err, string(out))
		}
		return outPath
	}

	// Helper to run ExtractWaveform and verify constraints
	verifyWaveform := func(path string, expectError bool, expectAllZero bool, description string) {
		t.Run(description, func(t *testing.T) {
			waveform, err := ExtractWaveform(path)
			if expectError {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
			}

			// Size must always be 64 bytes
			if len(waveform) != 64 {
				t.Errorf("expected waveform size 64, got %d", len(waveform))
			}

			// Verify peak value
			peak := byte(0)
			for _, v := range waveform {
				if v > peak {
					peak = v
				}
			}

			if expectAllZero {
				if peak != 0 {
					t.Errorf("expected all zero waveform (peak 0), got peak %d. Waveform: %v", peak, waveform)
				}
			} else {
				if peak != 255 {
					t.Errorf("expected peak value 255, got %d. Waveform: %v", peak, waveform)
				}
			}
		})
	}

	// Case 1: Empty file (size 0)
	emptyPath := filepath.Join(tempDir, "empty.ogg")
	if err := os.WriteFile(emptyPath, []byte{}, 0644); err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}
	// ffmpeg fails on empty file, so expectError = true
	verifyWaveform(emptyPath, true, true, "Empty File (0 bytes)")

	// Case 2: Corrupt file (random bytes)
	corruptPath := filepath.Join(tempDir, "corrupt.ogg")
	if err := os.WriteFile(corruptPath, []byte("invalid ogg opus data containing garbage"), 0644); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}
	// ffmpeg fails on corrupt file, so expectError = true
	verifyWaveform(corruptPath, true, true, "Corrupt File")

	// Case 3: Extremely short file (0.01 seconds)
	shortPath1 := generateOgg("short1.ogg", "sine=frequency=1000:duration=0.01")
	verifyWaveform(shortPath1, false, false, "Extremely short file 0.01s")

	// Case 4: Short file (0.5 seconds)
	shortPath2 := generateOgg("short2.ogg", "sine=frequency=1000:duration=0.5")
	verifyWaveform(shortPath2, false, false, "Short file 0.5s")

	// Case 5: Silent file (1 second of silence)
	silentPath := generateOgg("silent.ogg", "anullsrc=r=8000:cl=mono:d=1")
	verifyWaveform(silentPath, false, true, "Silent File")

	// Case 6: Extreme volume differences - Very loud signal then silence
	loudQuietPath2 := generateOgg("loud_quiet2.ogg", "aevalsrc=if(lt(t\\,0.5)\\,sin(2*PI*1000*t)\\,0):d=1")
	verifyWaveform(loudQuietPath2, false, false, "Loud then silent file")

	// Case 7: Extremely quiet file (sine wave with scale 0.01)
	quietPath := generateOgg("quiet.ogg", "sine=frequency=1000:duration=1, volume=0.01")
	verifyWaveform(quietPath, false, false, "Extremely quiet file")
}

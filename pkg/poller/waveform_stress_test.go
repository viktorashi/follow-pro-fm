//go:build e2e

package poller

import (
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExtractWaveform_Stress(t *testing.T) {
	const sampleRate = 8000

	// 1. Create a temp directory for test files
	tempDir, err := os.MkdirTemp("", "waveform_stress")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	findBundledFFmpeg := func() string {
		t.Helper()

		ffmpegPath, err := ffmpegBinaryPath()
		if err == nil {
			return ffmpegPath
		}
		return "ffmpeg"
	}

	writePCM := func(name string, durationSeconds float64, sample func(i int, t float64) float64) string {
		t.Helper()

		sampleCount := max(int(math.Round(durationSeconds*sampleRate)), 0)

		pcm := make([]byte, sampleCount*2)
		for i := range sampleCount {
			timestamp := float64(i) / sampleRate
			value := sample(i, timestamp)
			if value > 1 {
				value = 1
			}
			if value < -1 {
				value = -1
			}
			binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(math.Round(value*32767))))
		}

		outPath := filepath.Join(tempDir, name)
		if err := os.WriteFile(outPath, pcm, 0o644); err != nil {
			t.Fatalf("failed to write pcm %s: %v", name, err)
		}
		return outPath
	}

	// Helper function to generate an ogg file with specific audio properties using system ffmpeg
	generateOgg := func(name string, pcmPath string) string {
		t.Helper()

		outPath := filepath.Join(tempDir, name)
		cmd := exec.Command(findBundledFFmpeg(), "-y", "-f", "s16le", "-ar", "8000", "-ac", "1", "-i", pcmPath, "-strict", "-2", "-c:a", "opus", "-b:a", "64k", "-f", "ogg", outPath)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to generate ogg %s from %s: %v (output: %s)", name, pcmPath, err, string(out))
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
	if err := os.WriteFile(emptyPath, []byte{}, 0o644); err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}
	// ffmpeg fails on empty file, so expectError = true
	verifyWaveform(emptyPath, true, true, "Empty File (0 bytes)")

	// Case 2: Corrupt file (random bytes)
	corruptPath := filepath.Join(tempDir, "corrupt.ogg")
	if err := os.WriteFile(corruptPath, []byte("invalid ogg opus data containing garbage"), 0o644); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}
	// ffmpeg fails on corrupt file, so expectError = true
	verifyWaveform(corruptPath, true, true, "Corrupt File")

	// Case 3: Extremely short file (0.01 seconds)
	shortPath1 := generateOgg("short1.ogg", writePCM("short1.pcm", 0.01, func(_ int, ts float64) float64 {
		return math.Sin(2 * math.Pi * 1000 * ts)
	}))
	verifyWaveform(shortPath1, false, false, "Extremely short file 0.01s")

	// Case 4: Short file (0.5 seconds)
	shortPath2 := generateOgg("short2.ogg", writePCM("short2.pcm", 0.5, func(_ int, ts float64) float64 {
		return math.Sin(2 * math.Pi * 1000 * ts)
	}))
	verifyWaveform(shortPath2, false, false, "Short file 0.5s")

	// Case 5: Silent file (1 second of silence)
	silentPath := generateOgg("silent.ogg", writePCM("silent.pcm", 1, func(_ int, _ float64) float64 {
		return 0
	}))
	verifyWaveform(silentPath, false, true, "Silent File")

	// Case 6: Extreme volume differences - Very loud signal then silence
	loudQuietPath2 := generateOgg("loud_quiet2.ogg", writePCM("loud_quiet2.pcm", 1, func(_ int, ts float64) float64 {
		if ts < 0.5 {
			return math.Sin(2 * math.Pi * 1000 * ts)
		}
		return 0
	}))
	verifyWaveform(loudQuietPath2, false, false, "Loud then silent file")

	// Case 7: Extremely quiet file (sine wave with scale 0.01)
	quietPath := generateOgg("quiet.ogg", writePCM("quiet.pcm", 1, func(_ int, ts float64) float64 {
		return 0.01 * math.Sin(2*math.Pi*1000*ts)
	}))
	verifyWaveform(quietPath, false, false, "Extremely quiet file")
}

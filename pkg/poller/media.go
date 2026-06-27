package poller

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// GetAudioDuration uses ffprobe when available, then falls back to the tiny
// built-in parsers for formats we already understand exactly.
func GetAudioDuration(path string) (time.Duration, error) {
	if duration, err := getFFprobeDuration(path); err == nil {
		return duration, nil
	}

	ext := strings.ToLower(filepath.Ext(path))

	switch ext {
	case ".ogg":
		return getOggDuration(path)
	case ".wav":
		return getWavDuration(path)
	default:
		return 0, fmt.Errorf("unsupported audio duration format: %s", ext)
	}
}

func getFFprobeDuration(path string) (time.Duration, error) {
	ffprobePath, err := ffprobeBinaryPath()
	if err != nil {
		return 0, err
	}

	cmd := exec.Command(ffprobePath, "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", path)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	seconds, err := strconv.ParseFloat(strings.TrimSpace(stdout.String()), 64)
	if err != nil {
		return 0, fmt.Errorf("ffprobe returned invalid duration %q: %w", strings.TrimSpace(stdout.String()), err)
	}

	return time.Duration(seconds * float64(time.Second)), nil
}

func getOggDuration(path string) (time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = f.Close()
	}()

	stat, err := f.Stat()
	if err != nil {
		return 0, err
	}

	size := stat.Size()
	if size == 0 {
		return 0, fmt.Errorf("file is empty")
	}

	// Read the last 64KB (or up to file size) to find the last Ogg page
	chunkSize := int64(65536)
	if size < chunkSize {
		chunkSize = size
	}

	buf := make([]byte, chunkSize)
	_, err = f.ReadAt(buf, size-chunkSize)
	if err != nil && err != io.EOF {
		return 0, fmt.Errorf("failed to read end of ogg file: %w", err)
	}

	// Find the last "OggS" in the buffer
	lastIdx := bytes.LastIndex(buf, []byte("OggS"))
	if lastIdx == -1 {
		return 0, fmt.Errorf("no OggS magic found in last 64KB")
	}

	// The granule position is 6 bytes after the "OggS" magic
	if lastIdx+14 > len(buf) {
		return 0, fmt.Errorf("ogg page header truncated")
	}

	granulePos := binary.LittleEndian.Uint64(buf[lastIdx+6 : lastIdx+14])

	// Ogg Opus has a fixed sample rate of 48000 Hz.
	// For Ogg Vorbis, the sample rate varies, but WhatsApp strictly uses Opus at 48kHz.
	// We'll assume 48000 Hz for Ogg duration.
	durationSec := float64(granulePos) / 48000.0
	return time.Duration(durationSec * float64(time.Second)), nil
}

func getWavDuration(path string) (time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = f.Close()
	}()

	// Parse basic RIFF header
	// RIFF (4), Size (4), WAVE (4)
	buf := make([]byte, 12)
	if _, err := io.ReadFull(f, buf); err != nil {
		return 0, err
	}
	if string(buf[0:4]) != "RIFF" || string(buf[8:12]) != "WAVE" {
		return 0, fmt.Errorf("not a valid wav file")
	}

	var byteRate uint32
	var dataSize uint32

	// Search for fmt and data chunks (read up to 1024 bytes to find them)
	headerBuf := make([]byte, 1024)
	n, _ := io.ReadFull(f, headerBuf)

	for i := 0; i < n-8; {
		chunkID := string(headerBuf[i : i+4])
		chunkSize := binary.LittleEndian.Uint32(headerBuf[i+4 : i+8])

		if chunkID == "fmt " {
			if i+8+16 <= n {
				byteRate = binary.LittleEndian.Uint32(headerBuf[i+8+8 : i+8+12])
			}
		} else if chunkID == "data" {
			dataSize = chunkSize
			break
		}

		i += 8 + int(chunkSize)
		// Handle padding
		if chunkSize%2 != 0 {
			i++
		}
	}

	if byteRate == 0 || dataSize == 0 {
		return 0, fmt.Errorf("could not find fmt or data chunk in WAV")
	}

	durationSec := float64(dataSize) / float64(byteRate)
	return time.Duration(durationSec * float64(time.Second)), nil
}

func ffmpegBinaryPath() (string, error) {
	return resolveMediaBinary("FFMPEG_BIN", "ffmpeg")
}

func ffprobeBinaryPath() (string, error) {
	return resolveMediaBinary("FFPROBE_BIN", "ffprobe")
}

func resolveMediaBinary(envVar string, binName string) (string, error) {
	if explicit := os.Getenv(envVar); explicit != "" {
		return explicit, nil
	}

	if _, sourceFile, _, ok := runtime.Caller(0); ok {
		repoRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
		candidate := filepath.Join(repoRoot, "bin", binName)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return exec.LookPath(binName)
}

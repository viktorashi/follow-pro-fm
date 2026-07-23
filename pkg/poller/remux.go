package poller

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
)

// RemuxToMP3 pipes raw MP3 bytes through ffmpeg to generate proper MP3 framing.
// We must write to a temp file instead of a pipe so ffmpeg can seek back and write the Xing/Info header (needed for browser duration).
func RemuxToMP3(data []byte) ([]byte, error) {
	ffmpegPath, err := ffmpegBinaryPath()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}

	tmpFile, err := os.CreateTemp("", "remux-*.mp3")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	_ = tmpFile.Close() // ffmpeg will overwrite it
	defer func() { _ = os.Remove(tmpName) }()

	cmd := exec.Command(ffmpegPath, "-y", "-i", "pipe:0", "-c", "copy", "-f", "mp3", tmpName)
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg remux failed: %w (stderr: %s)", err, stderr.String())
	}

	outData, err := os.ReadFile(tmpName)
	if err != nil {
		return nil, fmt.Errorf("failed to read remuxed file: %w", err)
	}

	return outData, nil
}

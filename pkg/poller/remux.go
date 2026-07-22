package poller

import (
	"bytes"
	"fmt"
	"os/exec"
)

// RemuxToMP3 pipes raw MP3 bytes through ffmpeg to generate proper MP3 framing.
func RemuxToMP3(data []byte) ([]byte, error) {
	ffmpegPath, err := ffmpegBinaryPath()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg not found: %w", err)
	}

	cmd := exec.Command(ffmpegPath, "-i", "pipe:0", "-c", "copy", "-f", "mp3", "pipe:1")
	cmd.Stdin = bytes.NewReader(data)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg remux failed: %w (stderr: %s)", err, stderr.String())
	}

	return stdout.Bytes(), nil
}

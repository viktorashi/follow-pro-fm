package poller

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// NewLocalWhisperTranscriber creates a transcriber that uses the local whisper-cli binary
// and ffmpeg to convert the audio to 16kHz WAV first.
func NewLocalWhisperTranscriber(whisperBin, modelPath string) func(context.Context, []byte) (string, error) {
	return func(ctx context.Context, audioData []byte) (string, error) {
		tempDir, err := os.MkdirTemp("", "whisper-*")
		if err != nil {
			return "", fmt.Errorf("failed to create temp dir: %w", err)
		}
		defer func() { _ = os.RemoveAll(tempDir) }()

		inputPath := filepath.Join(tempDir, "input.audio")
		if err := os.WriteFile(inputPath, audioData, 0644); err != nil {
			return "", fmt.Errorf("failed to write input audio: %w", err)
		}

		wavPath := filepath.Join(tempDir, "output.wav")
		ffmpegCmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", inputPath, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wavPath)
		if out, err := ffmpegCmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("ffmpeg conversion failed: %w, output: %s", err, string(out))
		}

		// Run whisper-cli
		// -m <model> -f <wav> -nt (no timestamps) -l auto (auto detect language)
		whisperCmd := exec.CommandContext(ctx, whisperBin, "-m", modelPath, "-f", wavPath, "-nt")
		var outBuf bytes.Buffer
		whisperCmd.Stdout = &outBuf
		whisperCmd.Stderr = os.Stderr // pipe stderr to main app for debugging if needed

		if err := whisperCmd.Run(); err != nil {
			return "", fmt.Errorf("whisper-cli failed: %w", err)
		}

		transcript := strings.TrimSpace(outBuf.String())
		return transcript, nil
	}
}

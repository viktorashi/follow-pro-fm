package poller

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transcriptionTestCase struct {
	Name              string
	AudioBytes        []byte
	AudioPath         string
	TrustedTranscript string
}

func loadTranscriptionCases(t *testing.T) []transcriptionTestCase {
	t.Helper()

	baseDir := filepath.Join("testdata", "transcription", "cases")
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatalf("ReadDir(%s) error = %v", baseDir, err)
	}

	var cases []transcriptionTestCase
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dirPath := filepath.Join(baseDir, entry.Name())

		// Read audio file
		var audioBytes []byte
		var audioPath string
		dirEntries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, de := range dirEntries {
			ext := strings.ToLower(filepath.Ext(de.Name()))
			if ext == ".ogg" || ext == ".mp3" || ext == ".wav" {
				audioPath = filepath.Join(dirPath, de.Name())
				audioBytes, err = os.ReadFile(audioPath)
				if err != nil {
					t.Fatalf("ReadFile(%s) error = %v", audioPath, err)
				}
				break
			}
		}
		if len(audioBytes) == 0 {
			t.Fatalf("no audio file found in %s", dirPath)
		}

		// Read trusted transcript.txt
		txtPath := filepath.Join(dirPath, "transcript.txt")
		trustedText, err := os.ReadFile(txtPath)
		if err != nil {
			t.Fatalf("missing transcript.txt in %s", dirPath)
		}

		cases = append(cases, transcriptionTestCase{
			Name:              entry.Name(),
			AudioBytes:        audioBytes,
			AudioPath:         audioPath,
			TrustedTranscript: strings.TrimSpace(string(trustedText)),
		})
	}
	return cases
}

func TestWhisperContainerTranscription(t *testing.T) {
	// Check if docker daemon is available
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker daemon not available, skipping Whisper container transcription E2E test")
	}

	// Pick a dynamic free port on localhost
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	freePort := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	containerName := fmt.Sprintf("profm-whisper-test-%d", time.Now().UnixNano())
	imageName := "fedirz/faster-whisper-server:latest-cpu"

	t.Logf("Spinning up test Whisper container %s on port %d...", containerName, freePort)

	runCmd := exec.Command("docker", "run", "-d", "--rm",
		"-p", fmt.Sprintf("%d:8000", freePort),
		"--name", containerName,
		imageName,
	)
	if out, err := runCmd.CombinedOutput(); err != nil {
		t.Fatalf("docker run failed: %v, output: %s", err, string(out))
	}

	t.Cleanup(func() {
		t.Logf("Cleaning up test container %s...", containerName)
		_ = exec.Command("docker", "stop", containerName).Run()
	})

	// Wait for container readiness via /health endpoint
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", freePort)
	healthy := false
	for i := 0; i < 40; i++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			healthy = true
			break
		}
		if resp != nil {
			_ = resp.Body.Close()
		}
	}
	if !healthy {
		t.Fatalf("Whisper container failed to become healthy at %s", healthURL)
	}

	transcriptionURL := fmt.Sprintf("http://127.0.0.1:%d/v1/audio/transcriptions", freePort)
	transcriber := NewHTTPTranscriber(transcriptionURL)

	cases := loadTranscriptionCases(t)
	if len(cases) == 0 {
		t.Fatal("no transcription test cases found in testdata/transcription/cases")
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			liveTranscript, err := transcriber(ctx, tc.AudioBytes)
			if err != nil {
				t.Fatalf("Transcribe failed for %s: %v", tc.Name, err)
			}
			t.Logf("[%s] Live transcript: %q | Trusted transcript: %q", tc.Name, liveTranscript, tc.TrustedTranscript)

			if liveTranscript == "" {
				t.Fatalf("[%s] Got empty transcription from Whisper", tc.Name)
			}

			// Use the actual application matching logic (TriggerValuesMatch)
			if !TriggerValuesMatch(liveTranscript, tc.TrustedTranscript) {
				t.Fatalf("[%s] App TriggerValuesMatch(%q, %q) = false, want true", tc.Name, liveTranscript, tc.TrustedTranscript)
			}
		})
	}
}

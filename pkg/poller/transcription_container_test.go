package poller

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type transcriptionCaseConfig struct {
	Language         string
	ExpectedContains []string
}

type transcriptionTestCase struct {
	Name             string
	AudioBytes       []byte
	AudioPath        string
	ExpectedText     string
	ExpectedContains []string
	Language         string
}

func loadTranscriptionCaseConfig(t *testing.T, path string) transcriptionCaseConfig {
	t.Helper()

	cfg := transcriptionCaseConfig{Language: "ro"}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open(%s) error = %v", path, err)
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)

		switch key {
		case "language":
			cfg.Language = strings.Trim(value, `"`)
		case "expected_contains":
			// parse array like ["word1", "word2"]
			val := strings.Trim(value, "[]")
			parts := strings.Split(val, ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				p = strings.Trim(p, `"`)
				if p != "" {
					cfg.ExpectedContains = append(cfg.ExpectedContains, strings.ToLower(p))
				}
			}
		}
	}
	return cfg
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

		// Read transcript.txt if present
		txtPath := filepath.Join(dirPath, "transcript.txt")
		var expectedText string
		if data, err := os.ReadFile(txtPath); err == nil {
			expectedText = strings.TrimSpace(string(data))
		}

		// Read case.toml if present
		cfgPath := filepath.Join(dirPath, "case.toml")
		var cfg transcriptionCaseConfig
		if _, err := os.Stat(cfgPath); err == nil {
			cfg = loadTranscriptionCaseConfig(t, cfgPath)
		}

		cases = append(cases, transcriptionTestCase{
			Name:             entry.Name(),
			AudioBytes:       audioBytes,
			AudioPath:        audioPath,
			ExpectedText:     expectedText,
			ExpectedContains: cfg.ExpectedContains,
			Language:         cfg.Language,
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

			transcript, err := transcriber(ctx, tc.AudioBytes)
			if err != nil {
				t.Fatalf("Transcribe failed for %s: %v", tc.Name, err)
			}
			t.Logf("[%s] Transcribed result: %q (expected: %q)", tc.Name, transcript, tc.ExpectedText)

			if transcript == "" {
				t.Fatalf("[%s] Got empty transcription", tc.Name)
			}

			// Verify expected_contains keywords if defined
			lowerTranscript := strings.ToLower(transcript)
			for _, kw := range tc.ExpectedContains {
				if !strings.Contains(lowerTranscript, kw) {
					t.Fatalf("[%s] Transcription %q does not contain expected phrase %q", tc.Name, transcript, kw)
				}
			}
		})
	}
}

// Suppress unused strconv import warning if needed
var _ = strconv.Itoa

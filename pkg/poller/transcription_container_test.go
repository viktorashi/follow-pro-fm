//go:build e2e
// +build e2e

package poller

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type transcriptionTestCase struct {
	Name           string
	AudioBytes     []byte
	AudioPath      string
	FullText       string
	TrustedPhrases []string
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

		// Read radio stream audio file (stream.mp3 or .mp3 / .ogg / .wav)
		var audioBytes []byte
		var audioPath string
		dirEntries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, de := range dirEntries {
			ext := strings.ToLower(filepath.Ext(de.Name()))
			if ext == ".mp3" || ext == ".ogg" || ext == ".wav" {
				audioPath = filepath.Join(dirPath, de.Name())
				audioBytes, err = os.ReadFile(audioPath)
				if err != nil {
					t.Fatalf("ReadFile(%s) error = %v", audioPath, err)
				}
				break
			}
		}
		if len(audioBytes) == 0 {
			t.Fatalf("no stream audio file found in %s", dirPath)
		}

		// Read trusted transcript.txt containing full text and campaign phrases
		txtPath := filepath.Join(dirPath, "transcript.txt")
		trustedData, err := os.ReadFile(txtPath)
		if err != nil {
			t.Fatalf("missing transcript.txt in %s", dirPath)
		}

		rawLines := strings.Split(string(trustedData), "\n")
		var fullText string
		var phrases []string
		for _, line := range rawLines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if fullText == "" {
				fullText = line
			}
			phrases = append(phrases, line)
		}

		cases = append(cases, transcriptionTestCase{
			Name:           entry.Name(),
			AudioBytes:     audioBytes,
			AudioPath:      audioPath,
			FullText:       fullText,
			TrustedPhrases: phrases,
		})
	}
	return cases
}

func TestWhisperContainerTranscription(t *testing.T) {
	t.Setenv("BYPASS_CAMPAIGN_TIME_CHECKS", "true")
	// Check if docker daemon is available
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker daemon not available, skipping Whisper container transcription E2E test")
	}

	containerName := "profm-whisper-test"
	testPort := 18000
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", testPort)
	transcriptionURL := fmt.Sprintf("http://127.0.0.1:%d/v1/audio/transcriptions", testPort)

	// Probe existing container if running to see if it is fully functional
	alreadyHealthy := false
	resp, err := http.Get(healthURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		_ = resp.Body.Close()
		// Test actual transcription endpoint to ensure worker thread is alive (not EOFing)
		probeTranscriber := NewHTTPTranscriber(transcriptionURL)
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, probeErr := probeTranscriber(probeCtx, []byte("fake_audio"))
		probeCancel()
		if probeErr == nil || (!strings.Contains(probeErr.Error(), "EOF") && !strings.Contains(probeErr.Error(), "connection refused")) {
			alreadyHealthy = true
		}
	}

	if alreadyHealthy {
		t.Logf("Reusing existing healthy Whisper test container %s at %s", containerName, healthURL)
	} else {
		// Force-remove any stale/dead container before starting a fresh instance
		_ = exec.Command("docker", "rm", "-f", containerName).Run()

		imageName := "fedirz/faster-whisper-server:latest-cpu"
		t.Logf("Spinning up test Whisper container %s on port %d...", containerName, testPort)

		// Use a local directory bind mount so GitHub Actions can cache it
		cacheDir, err := filepath.Abs("../../.whisper_cache")
		if err != nil {
			t.Fatalf("failed to resolve cache dir: %v", err)
		}
		if err := os.MkdirAll(cacheDir, 0o755); err != nil {
			t.Fatalf("failed to create cache dir: %v", err)
		}

		runCmd := exec.Command(
			"docker", "run", "-d",
			"-p", fmt.Sprintf("%d:8000", testPort),
			"-v", fmt.Sprintf("%s:/root/.cache/huggingface", cacheDir),
			"-e", "UVICORN_HOST=0.0.0.0",
			"-e", "ENABLE_UI=false",
			"-e", "WHISPER_MODEL=base",
			"-e", "WHISPER__MODEL=base",
			"-e", "WHISPER__COMPUTE_TYPE=int8",
			"-e", "DEFAULT_LANGUAGE=ro",
			"-e", "OMP_NUM_THREADS=4",
			"--name", containerName,
			imageName,
		)
		if out, err := runCmd.CombinedOutput(); err != nil {
			t.Fatalf("docker run failed: %v, output: %s", err, string(out))
		}

		// Wait for container readiness via /health endpoint
		healthy := false
		for i := 0; i < 240; i++ {
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
	}
	transcriber := NewHTTPTranscriber(transcriptionURL)

	cases := loadTranscriptionCases(t)
	if len(cases) == 0 {
		t.Fatal("no transcription test cases found in testdata/transcription/cases")
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
			defer cancel()

			liveTranscript, err := runProductionTranscriptionCheck(ctx, transcriber, tc, filepath.Join(t.TempDir(), "phrases.sqlite"))
			if err != nil {
				t.Fatalf("Transcribe failed for %s: %v", tc.Name, err)
			}
			t.Logf("[%s] Live transcript: %q | Full text: %q", tc.Name, liveTranscript, tc.FullText)

			if liveTranscript == "" {
				t.Fatalf("[%s] Got empty transcription from Whisper", tc.Name)
			}

		})
	}
}

func runProductionTranscriptionCheck(ctx context.Context, transcriber func(context.Context, []byte) (string, error), tc transcriptionTestCase, dbPath string) (string, error) {
	db, err := NewDBManager(dbPath)
	if err != nil {
		return "", err
	}
	for _, phrase := range tc.TrustedPhrases {
		if err := db.AddCampaignPhrase(context.Background(), "Test Campaign", phrase); err != nil {
			return "", err
		}
	}

	buffer := NewCircularAudioBuffer("", transcriptionTailBytes*2)
	alerter := &recordingAlerter{}
	lastTranscript := ""
	poller := &Poller{
		ActiveCampaigns:     []Campaign{{Artist: "Test Campaign"}},
		AudioBuffer:         buffer,
		TranscriptionBuffer: NewTimeSeriesBuffer[string](10 * time.Minute),
		DBMgr:               db,
		Alerter:             alerter,
		StateMgr:            createMockStateMgr(),
		Transcribe: func(ctx context.Context, audio []byte) (string, error) {
			transcript, err := transcriber(ctx, audio)
			lastTranscript = transcript
			return transcript, err
		},
	}
	checker := &transcriptionContestChecker{poller: poller, coordinator: NewContestCheckCoordinator(time.Minute)}
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, bucharestLocation)

	for start := 0; start < len(tc.AudioBytes); start += transcriptionTailBytes {
		end := start + transcriptionTailBytes
		if end > len(tc.AudioBytes) {
			end = len(tc.AudioBytes)
		}
		buffer.writeBytes(tc.AudioBytes[start:end])
		checker.Check(now)
		if len(alerter.successEvents) > 0 {
			return lastTranscript, nil
		}
		if err := ctx.Err(); err != nil {
			return lastTranscript, err
		}
		now = now.Add(contestCaptureWindow)
	}
	return lastTranscript, fmt.Errorf("production transcription checker did not trigger")
}

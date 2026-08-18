//go:build e2e
// +build e2e

package poller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

var errNoTrustedPhrase = errors.New("audio finished before a trusted phrase matched")

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
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("Docker daemon not available, skipping Whisper container transcription E2E test")
	}

	containerName := "profm-whisper-test"
	imageName := "profm-whisper-test:local"
	testPort := 18000
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", testPort)
	transcriptionURL := fmt.Sprintf("http://127.0.0.1:%d/v1/audio/transcriptions", testPort)

	if os.Getenv("WHISPER_TEST_IMAGE_READY") != "1" {
		if out, err := exec.Command("docker", "build", "-q", "-t", imageName, "../../whisper-server").CombinedOutput(); err != nil {
			t.Fatalf("docker build failed: %v, output: %s", err, out)
		}
	}
	imageID, err := exec.Command("docker", "image", "inspect", imageName, "--format", "{{.Id}}").Output()
	if err != nil {
		t.Fatalf("docker image inspect failed: %v", err)
	}
	containerImageID, _ := exec.Command("docker", "inspect", containerName, "--format", "{{.Image}}").Output()
	alreadyHealthy := strings.TrimSpace(string(imageID)) == strings.TrimSpace(string(containerImageID)) && whisperHealthy(healthURL)
	if !alreadyHealthy {
		_ = exec.Command("docker", "rm", "-f", containerName).Run()
		out, err := exec.Command("docker", "run", "-d", "-p", fmt.Sprintf("%d:8000", testPort), "--name", containerName, imageName).CombinedOutput()
		if err != nil {
			t.Fatalf("docker run failed: %v, output: %s", err, out)
		}
		for deadline := time.Now().Add(2 * time.Minute); !whisperHealthy(healthURL); {
			if time.Now().After(deadline) {
				t.Fatalf("Whisper container failed to become healthy at %s", healthURL)
			}
			time.Sleep(500 * time.Millisecond)
		}
	}

	cases := loadTranscriptionCases(t)
	if len(cases) == 0 {
		t.Fatal("no transcription test cases found in testdata/transcription/cases")
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			var liveTranscript string
			for attempt := 1; attempt <= 2; attempt++ {
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				var err error
				liveTranscript, err = runProductionWebSocketCheck(ctx, transcriptionURL, tc, filepath.Join(t.TempDir(), "phrases.sqlite"))
				cancel()
				if err == nil {
					break
				}
				if attempt == 2 || !errors.Is(err, errNoTrustedPhrase) {
					t.Fatalf("streaming transcription failed for %s: %v; transcript: %q", tc.Name, err, liveTranscript)
				}
				t.Logf("[%s] retrying nondeterministic transcript: %q", tc.Name, liveTranscript)
			}
			t.Logf("[%s] Live transcript: %q | Full text: %q", tc.Name, liveTranscript, tc.FullText)

			if liveTranscript == "" {
				t.Fatalf("[%s] Got empty transcription from Whisper", tc.Name)
			}

		})
	}
}

func whisperHealthy(url string) bool {
	resp, err := http.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

type transcriptionTriggerAlerter struct {
	once      sync.Once
	triggered chan struct{}
}

func (a *transcriptionTriggerAlerter) AlertCritical(AlertEvent) error { return nil }
func (a *transcriptionTriggerAlerter) AlertInfo(AlertEvent) error     { return nil }
func (a *transcriptionTriggerAlerter) AlertSuccess(AlertEvent) error {
	a.once.Do(func() { close(a.triggered) })
	return nil
}

func runProductionWebSocketCheck(ctx context.Context, transcriptionURL string, tc transcriptionTestCase, dbPath string) (string, error) {
	db, err := NewDBManager(dbPath)
	if err != nil {
		return "", err
	}
	for _, phrase := range tc.TrustedPhrases {
		if err := db.AddCampaignPhrase(context.Background(), "Test Campaign", phrase); err != nil {
			return "", err
		}
	}

	alerter := &transcriptionTriggerAlerter{triggered: make(chan struct{})}
	poller := &Poller{
		ActiveCampaigns:     []Campaign{{Artist: "Test Campaign"}},
		TranscriptionBuffer: NewTimeSeriesBuffer[string](10 * time.Minute),
		DBMgr:               db,
		Alerter:             alerter,
		StateMgr:            createMockStateMgr(),
	}
	var transcriptMu sync.Mutex
	var transcripts []string
	mp3 := make(chan []byte)
	transcriber := StartStreamingTranscription(ctx, mp3, transcriptionURL, func(transcript string) {
		transcriptMu.Lock()
		transcripts = append(transcripts, transcript)
		transcriptMu.Unlock()
		poller.HandleStreamingTranscript(transcript)
	})
	go func() {
		defer close(mp3)
		const streamChunkBytes = 8192
		for start := 0; start < len(tc.AudioBytes); start += streamChunkBytes {
			end := start + streamChunkBytes
			if end > len(tc.AudioBytes) {
				end = len(tc.AudioBytes)
			}
			select {
			case mp3 <- tc.AudioBytes[start:end]:
			case <-ctx.Done():
				return
			}
		}
	}()

	result := func() string {
		transcriptMu.Lock()
		defer transcriptMu.Unlock()
		return strings.Join(transcripts, " | ")
	}
	select {
	case <-alerter.triggered:
		return result(), nil
	case <-transcriber.Done():
		select {
		case <-alerter.triggered:
			return result(), nil
		default:
			return result(), errNoTrustedPhrase
		}
	case <-ctx.Done():
		return result(), ctx.Err()
	}
}

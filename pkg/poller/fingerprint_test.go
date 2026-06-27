package poller

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMatchSignature(t *testing.T) {
	if _, err := ffmpegBinaryPath(); err != nil {
		t.Skip("ffmpeg not installed, skipping audio fingerprint validation")
	}

	for _, tc := range loadFingerprintCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			stream := mustReadTestFile(t, "testdata", "fingerprint", tc.StreamFile)
			signature := mustReadTestFile(t, "testdata", "fingerprint", tc.SignatureFile)
			if got := MatchSignature(stream, signature); got != tc.ShouldMatch {
				t.Fatalf("MatchSignature() = %v, want %v", got, tc.ShouldMatch)
			}
		})
	}
}

func TestFingerprintCasesDriveVoiceNoteTriggerExpectations(t *testing.T) {
	if _, err := ffmpegBinaryPath(); err != nil {
		t.Skip("ffmpeg not installed, skipping audio fingerprint validation")
	}

	for _, tc := range loadFingerprintCases(t) {
		t.Run(tc.Name, func(t *testing.T) {
			canonicalDir := t.TempDir()
			stream := mustReadTestFile(t, "testdata", "fingerprint", tc.StreamFile)
			signature := mustReadTestFile(t, "testdata", "fingerprint", tc.SignatureFile)
			if err := os.WriteFile(filepath.Join(canonicalDir, tc.CanonicalName), signature, 0o644); err != nil {
				t.Fatalf("WriteFile(canonical) error = %v", err)
			}

			matched, matchedName, err := findMatchingCanonicalSignature(stream, defaultFingerprintFormat, canonicalDir)
			if err != nil {
				t.Fatalf("findMatchingCanonicalSignature() error = %v", err)
			}
			if matched != tc.ShouldMatch {
				t.Fatalf("matched = %v, want %v", matched, tc.ShouldMatch)
			}

			audiosDir := t.TempDir()
			testAudio := mustReadTestFile(t, "testdata", "waveform_sample.ogg")
			if err := os.WriteFile(filepath.Join(audiosDir, "sample.ogg"), testAudio, 0o644); err != nil {
				t.Fatalf("WriteFile(audio) error = %v", err)
			}

			sendCount := 0
			poller := &Poller{
				ActiveCampaigns: []Campaign{
					{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
				},
				TargetPhone: "+40770661491",
				StateMgr:    NewStateManager(),
				Alerter:     &recordingAlerter{},
				AudiosDir:   audiosDir,
				SendVoiceNote: func(senderPhone string, targetPhone string, audioPath string) error {
					sendCount++
					return nil
				},
			}
			poller.StateMgr.Update(func(s *AppState) {
				s.Connections = []WAConnectionState{{Phone: CanonicalSenderPhone, Status: StatusConnected, WhatsAppConnected: true}}
			})

			if matched {
				trigger := parseFingerprintTrigger(matchedName)
				song := poller.resolveFingerprintSong(trigger)
				now := bucharestTime(2026, time.June, 17, 12, 0, 0)
				campaignArtist, ok := poller.matchingCampaignArtist(now, song)
				if ok {
					matchIndex, selected := poller.claimScheduledMatch(now, song.Artist, song.Title)
					if selected {
						poller.doTriggerVoiceNote(triggerSourceFingerprint, campaignArtist, song.Artist, song.Title, now, matchIndex, 0)
					}
				}
			}

			gotTrigger := sendCount == 1
			if gotTrigger != tc.ShouldTriggerVoiceNote {
				t.Fatalf("voice note trigger = %v, want %v", gotTrigger, tc.ShouldTriggerVoiceNote)
			}

			if tc.ShouldTriggerVoiceNote {
				used, err := os.Stat(filepath.Join(audiosDir, "used", "sample.ogg"))
				if err != nil || used.IsDir() {
					t.Fatalf("expected used audio rotation after trigger, err = %v", err)
				}
			}
		})
	}
}

func TestSaveUnreviewedChunkIfDistinctSkipsCanonical(t *testing.T) {
	unreviewedDir := t.TempDir()
	canonicalDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(canonicalDir, "known.mp3"), []byte("signature"), 0644); err != nil {
		t.Fatalf("Failed to seed canonical signature: %v", err)
	}

	saved, matchedName, err := SaveUnreviewedChunkIfDistinct([]byte("prefix-signature-suffix"), unreviewedDir, canonicalDir, "candidate.mp3")
	if err != nil {
		t.Fatalf("SaveUnreviewedChunkIfDistinct failed: %v", err)
	}
	if saved {
		t.Fatal("Expected duplicate chunk to be skipped")
	}
	if matchedName != "known.mp3" {
		t.Fatalf("Expected matched canonical name to be known.mp3, got %q", matchedName)
	}

	entries, err := os.ReadDir(unreviewedDir)
	if err != nil {
		t.Fatalf("Failed to read unreviewed dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("Expected no saved unreviewed chunks, found %d", len(entries))
	}
}

func TestCropAndMarkCanonicalPreservesOriginal(t *testing.T) {
	unreviewedDir := t.TempDir()
	canonicalDir := t.TempDir()

	filename := "test_chunk.mp3"
	data := []byte("0123456789")
	err := SaveUnreviewedChunk(data, unreviewedDir, filename)
	if err != nil {
		t.Fatalf("Failed to save unreviewed chunk: %v", err)
	}

	// Crop "3456"
	err = CropAndMarkCanonical(unreviewedDir, canonicalDir, filename, 3, 7)
	if err != nil {
		t.Fatalf("Crop failed: %v", err)
	}

	sigs, err := GetCanonicalSignatures(canonicalDir)
	if err != nil {
		t.Fatalf("Failed to get signatures: %v", err)
	}

	if len(sigs) != 1 {
		t.Fatalf("Expected 1 canonical signature, got %d", len(sigs))
	}

	if !bytes.Equal(sigs[filename], []byte("3456")) {
		t.Errorf("Expected cropped signature to be '3456', got '%s'", sigs[filename])
	}

	original, err := os.ReadFile(filepath.Join(unreviewedDir, filename))
	if err != nil {
		t.Fatalf("Expected original unreviewed chunk to remain: %v", err)
	}
	if !bytes.Equal(original, data) {
		t.Fatalf("Expected original unreviewed chunk to be preserved")
	}
}

func mustReadTestFile(t *testing.T, elems ...string) []byte {
	t.Helper()

	path := filepath.Join(elems...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Failed to read fixture %s: %v", path, err)
	}

	return data
}

type fingerprintCase struct {
	Name                   string `json:"name"`
	StreamFile             string `json:"stream_file"`
	SignatureFile          string `json:"signature_file"`
	CanonicalName          string `json:"canonical_name"`
	ShouldMatch            bool   `json:"should_match"`
	ShouldTriggerVoiceNote bool   `json:"should_trigger_voice_note"`
}

func loadFingerprintCases(t *testing.T) []fingerprintCase {
	t.Helper()

	raw := mustReadTestFile(t, "testdata", "fingerprint", "cases.json")
	var cases []fingerprintCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatalf("failed to parse fingerprint cases: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("expected fingerprint cases")
	}
	return cases
}

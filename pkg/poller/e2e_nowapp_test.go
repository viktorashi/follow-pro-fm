//go:build e2e
// +build e2e

package poller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPoller_E2E_NoWhatsApp(t *testing.T) {
	audiosDir := filepath.Join(t.TempDir(), "audios")
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to create in-memory DB: %v", err)
	}
	person, err := dbMgr.CreatePerson(context.Background(), "E2E Sender")
	if err != nil {
		t.Fatalf("Failed to create sender: %v", err)
	}
	const senderPhone = "+40700000000"
	if err := dbMgr.SetSenderSessionWithPerson(context.Background(), senderPhone, "wapp_40700000000.sqlite", &person.ID); err != nil {
		t.Fatalf("Failed to create sender session: %v", err)
	}
	audioDir := GetAudioDirForPerson(person.Slug, audiosDir)
	if err := InitAudioPool(audioDir); err != nil {
		t.Fatalf("Failed to initialize audio pool: %v", err)
	}
	audio, err := os.ReadFile("testdata/waveform_sample.ogg")
	if err != nil {
		t.Fatalf("Failed to read audio fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "voice.ogg"), audio, 0o644); err != nil {
		t.Fatalf("Failed to seed audio pool: %v", err)
	}

	server := NewCampaignHitServer(t)
	defer server.Close()
	activeTime := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)
	stateMgr := NewStateManager()
	stateMgr.Update(func(s *AppState) {
		s.Connections = []WAConnectionState{{Phone: senderPhone, WhatsAppConnected: true, Status: StatusConnected}}
	})
	HydrateConnectionPersons(dbMgr, stateMgr, context.Background())

	sent := 0
	poller := &Poller{
		APIURL:       server.URL,
		PollInterval: 1 * time.Millisecond,
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
		TargetPhone: "+40700000001",
		StateMgr:    stateMgr,
		Alerter:     &recordingAlerter{},
		AudiosDir:   audiosDir,
		DBMgr:       dbMgr,
		SendVoiceNote: func(senderPhone string, targetPhone string, audioPath string) error {
			sent++
			return nil
		},
	}

	currentSong := &SongInfo{}

	t.Log("Triggering song check...")
	(&metadataContestChecker{poller: poller, coordinator: poller.contestCheckCoordinator(), currentSong: currentSong}).Check(activeTime)

	if poller.matchesToday != 1 || sent != 1 {
		t.Fatalf("matches = %d, sends = %d; want one of each", poller.matchesToday, sent)
	}
}

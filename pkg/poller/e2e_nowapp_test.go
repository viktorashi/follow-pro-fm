//go:build e2e && nowapp
// +build e2e,nowapp

package poller

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPoller_E2E_NoWhatsApp(t *testing.T) {
	rootDir := E2EProjectRoot(t)
	audiosDir := filepath.Join(rootDir, "data/audios")
	LoadE2EEnv(rootDir)

	t.Log("Skipping real WhatsApp client initialization (nowapp build tag)")

	if err := InitAudioPool(audiosDir); err != nil {
		t.Fatalf("Failed to initialize audio pool: %v", err)
	}

	multiAlerter := E2EMultiAlerter(rootDir)
	targetPhone := E2ETargetPhoneFromEnv()

	activeTime := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)

	server := NewCampaignHitServer(t)
	defer server.Close()

	poller := &Poller{
		APIURL:       server.URL,
		PollInterval: 1 * time.Millisecond,
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
		TargetPhone: targetPhone,
		StateMgr:    NewStateManager(),
		Alerter:     multiAlerter,
		AudiosDir:   audiosDir,
		SendVoiceNote: func(senderPhone string, targetPhone string, audioPath string) error {
			t.Logf("🚀 Simulating voice note send from %s to %s (audio: %s)", senderPhone, targetPhone, audioPath)
			return nil
		},
	}
	poller.StateMgr.Update(func(s *AppState) {
		s.Connections = []WAConnectionState{{Phone: "+40734788254", WhatsAppConnected: true, Status: StatusConnected}}
	})

	currentSong := &SongInfo{}

	t.Log("Triggering song check...")
	(&metadataContestChecker{poller: poller, coordinator: poller.contestCheckCoordinator(), currentSong: currentSong}).Check(activeTime)

	if poller.matchesToday != 1 {
		t.Fatalf("Expected 1 match to trigger message, got %d", poller.matchesToday)
	}

	t.Log("E2E test complete! Check your phone for the voice note.")
}

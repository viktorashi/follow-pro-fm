//go:build e2e
// +build e2e

package poller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPoller_E2E(t *testing.T) {
	// 1. Initialize real WhatsApp client (will prompt for QR if not paired)
	t.Log("Initializing real WhatsApp client...")
	client, err := InitWhatsApp("../../tests/e2e.sqlite", nil)
	if err != nil {
		t.Fatalf("Failed to initialize WhatsApp: %v", err)
	}
	defer client.Disconnect()

	// bubu phfon
	targetPhone := "+40762631673"

	// A Wednesday at 12:00 PM (Active time for campaigns)
	activeTime := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)

	// Mock ProFM server that explicitly returns a campaign hit (BTS)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		epg := EPGData{}
		epg.Data.Epg.Title = "BTS"
		epg.Data.Epg.Subtitle = "Dynamite E2E Test"
		_ = json.NewEncoder(w).Encode(epg)
	}))
	defer server.Close()

	// Create poller with real WhatsApp send function
	poller := &Poller{
		APIURL:       server.URL,
		PollInterval: 1 * time.Millisecond,
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
		TargetPhone: targetPhone,
		StateMgr:    NewStateManager(),
		Alerter:     NewMultiAlerter(),
		AudiosDir:   "../../data/audios",
		SendVoiceNote: func(phone string, audioPath string) error {
			t.Logf("🚀 Triggering real E2E voice note send to %s...", phone)
			return SendVoiceNote(client, phone, "../../"+audioPath)
		},
	}

	currentSong := &SongInfo{}

	// Call checkSong once. Because the mock server returns BTS, it will trigger the voice note.
	t.Log("Triggering song check...")
	poller.checkSong(currentSong, activeTime)

	if poller.matchesToday != 1 {
		t.Fatalf("Expected 1 match to trigger message, got %d", poller.matchesToday)
	}

	t.Log("E2E test complete! Check your phone for the voice note.")
}

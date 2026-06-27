//go:build e2e && !nowapp
// +build e2e,!nowapp

package poller

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"
)

func TestPoller_E2E(t *testing.T) {
	rootDir := E2EProjectRoot(t)
	dbPath := filepath.Join(rootDir, "data/wapp.sqlite")
	audiosDir := filepath.Join(rootDir, "data/audios")
	LoadE2EEnv(rootDir)
	multiAlerter := E2EMultiAlerter(rootDir)

	t.Log("Initializing real WhatsApp client...")
	client, err := InitWhatsApp("+40734788254", dbPath, nil, multiAlerter, "")
	if err != nil {
		t.Fatalf("Failed to initialize WhatsApp: %v", err)
	}
	defer client.Disconnect()

	// Track when the message is delivered to prevent "Waiting for this message" E2E issue
	deliveredChan := make(chan struct{}, 1)
	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Receipt:
			t.Logf("   📥 Received receipt: Type=%s, Chat=%s, MessageIDs=%v", v.Type, v.Chat, v.MessageIDs)
			// Empty Type means "delivered" (types.ReceiptTypeDelivered)
			if v.Type == "" || v.Type == "read" {
				select {
				case deliveredChan <- struct{}{}:
				default:
				}
			}
		}
	})

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
			t.Logf("🚀 Triggering real E2E voice note send from %s to %s...", senderPhone, targetPhone)
			return SendVoiceNote(client, targetPhone, audioPath)
		},
	}
	poller.StateMgr.Update(func(s *AppState) {
		s.Connections = []WAConnectionState{{Phone: "+40734788254", WhatsAppConnected: true, Status: StatusConnected}}
	})

	currentSong := &SongInfo{}

	t.Log("Triggering song check...")
	poller.checkSong(currentSong, activeTime)

	if poller.matchesToday != 1 {
		t.Fatalf("Expected 1 match to trigger message, got %d", poller.matchesToday)
	}

	t.Log("🚀 Message sent! Waiting up to 60 seconds for recipient's delivery/read receipt (unlock/open WhatsApp on your phone to receive)...")
	select {
	case <-deliveredChan:
		t.Log("✅ Success! Recipient phone received/acknowledged the message. E2E keys are synchronized.")
		time.Sleep(3 * time.Second)
	case <-time.After(60 * time.Second):
		t.Log("⚠️ Timeout! Recipient phone did not acknowledge the message within 60 seconds. It might be offline or locked. The message may display 'Waiting for this message' on the recipient device.")
	}
}

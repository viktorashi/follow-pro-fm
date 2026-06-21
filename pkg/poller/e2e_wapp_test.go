//go:build e2e && !nowapp
// +build e2e,!nowapp

package poller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"go.mau.fi/whatsmeow/types/events"
)

const TARGET_PHONE = "+40770661491"

func TestPoller_E2E(t *testing.T) {
	// Dynamically compute the project root directory relative to this test file.
	_, filename, _, _ := runtime.Caller(0)
	rootDir := filepath.Join(filepath.Dir(filename), "../..")
	dbPath := filepath.Join(rootDir, "data/wapp.sqlite")
	audiosDir := filepath.Join(rootDir, "data/audios")
	envPath := filepath.Join(rootDir, ".env")

	// Load local .env variables
	_ = godotenv.Load(envPath)

	// 1. Setup Alerters from .env
	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	telegramChatID := os.Getenv("TELEGRAM_CHAT_ID")
	appEnv := os.Getenv("ENVIRONMENT")
	if appEnv == "" {
		appEnv = "e2e Testing"
	}
	tgAlerter := NewTelegramAlerter(telegramToken, telegramChatID, appEnv, "http://localhost:8080")

	sendgridKey := os.Getenv("SENDGRID_API_KEY")
	emailFrom := os.Getenv("EMAIL_FROM")
	emAlerter := NewEmailAlerter(sendgridKey, emailFrom, filepath.Join(rootDir, "data/trusted-emails.txt"), appEnv, "http://localhost:8080")

	multiAlerter := NewMultiAlerter(tgAlerter, emAlerter)

	// 2. Initialize real WhatsApp client (will prompt for QR if not paired)
	t.Log("Initializing real WhatsApp client...")
	client, err := InitWhatsApp(dbPath, nil, multiAlerter, "")
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

	// target phone
	targetPhone := os.Getenv("TARGET_PHONE")
	if targetPhone == "" {
		targetPhone = TARGET_PHONE
	}

	// A Wednesday at 12:00 PM (Active time for campaigns)
	activeTime := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)

	// Mock ProFM server that explicitly returns a campaign hit (BTS)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		epg := EPGData{}
		epg.Data.Epg.Title = "BTS"
		epg.Data.Epg.Subtitle = "Dynamite"
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
		Alerter:     multiAlerter,
		AudiosDir:   audiosDir,
		SendVoiceNote: func(phone string, audioPath string) error {
			t.Logf("🚀 Triggering real E2E voice note send to %s...", phone)
			return SendVoiceNote(client, phone, audioPath)
		},
	}

	currentSong := &SongInfo{}

	// Call checkSong once. Because the mock server returns BTS, it will trigger the voice note.
	t.Log("Triggering song check...")
	poller.checkSong(currentSong, activeTime)

	if poller.matchesToday != 1 {
		t.Fatalf("Expected 1 match to trigger message, got %d", poller.matchesToday)
	}

	t.Log("🚀 Message sent! Waiting up to 60 seconds for recipient's delivery/read receipt (unlock/open WhatsApp on your phone to receive)...")
	select {
	case <-deliveredChan:
		t.Log("✅ Success! Recipient phone received/acknowledged the message. E2E keys are synchronized.")
		// Wait a small extra buffer to ensure final packets are sent
		time.Sleep(3 * time.Second)
	case <-time.After(60 * time.Second):
		t.Log("⚠️ Timeout! Recipient phone did not acknowledge the message within 60 seconds. It might be offline or locked. The message may display 'Waiting for this message' on the recipient device.")
	}
}

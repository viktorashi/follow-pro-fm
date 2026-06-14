//go:build e2e && nowapp
// +build e2e,nowapp

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
)

func TestPoller_E2E_NoWhatsApp(t *testing.T) {
	// Dynamically compute the project root directory relative to this test file.
	_, filename, _, _ := runtime.Caller(0)
	rootDir := filepath.Join(filepath.Dir(filename), "../..")
	audiosDir := filepath.Join(rootDir, "data/audios")
	envPath := filepath.Join(rootDir, ".env")

	// Load local .env variables
	_ = godotenv.Load(envPath)

	t.Log("Skipping real WhatsApp client initialization (nowapp build tag)")

	// Initialize Audio Pool (creates the 'used' folder if it doesn't exist)
	if err := InitAudioPool(audiosDir); err != nil {
		t.Fatalf("Failed to initialize audio pool: %v", err)
	}

	// 2. Setup Alerters from .env
	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	telegramChatID := os.Getenv("TELEGRAM_CHAT_ID")
	tgAlerter := NewTelegramAlerter(telegramToken, telegramChatID)

	resendKey := os.Getenv("RESEND_API_KEY")
	emailFrom := os.Getenv("EMAIL_FROM")
	emAlerter := NewEmailAlerter(resendKey, emailFrom, filepath.Join(rootDir, "data/trusted-emails.txt"))

	multiAlerter := NewMultiAlerter(tgAlerter, emAlerter)

	// target phone
	targetPhone := os.Getenv("TARGET_PHONE")
	if targetPhone == "" {
		targetPhone = "+40762631673"
	}

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
		Alerter:     multiAlerter,
		AudiosDir:   audiosDir,
		SendVoiceNote: func(phone string, audioPath string) error {
			t.Logf("🚀 Simulating voice note send to %s (audio: %s)", phone, audioPath)
			return nil
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

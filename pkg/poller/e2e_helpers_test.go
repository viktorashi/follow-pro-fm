//go:build e2e
// +build e2e

package poller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/joho/godotenv"
)

const E2ETargetPhone = "+40770661491"

func E2EProjectRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() failed")
	}
	return filepath.Join(filepath.Dir(filename), "../..")
}

func LoadE2EEnv(rootDir string) {
	_ = godotenv.Load(filepath.Join(rootDir, ".env"))
}

func E2EMultiAlerter(rootDir string) Alerter {
	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	telegramChatID := os.Getenv("TELEGRAM_CHAT_ID")
	appEnv := "E2E-Testing"
	tgAlerter := NewTelegramAlerter(telegramToken, telegramChatID, appEnv, "http://localhost:8080")

	sendgridKey := os.Getenv("SENDGRID_API_KEY")
	emailFrom := os.Getenv("EMAIL_FROM")
	emAlerter := NewEmailAlerter(sendgridKey, emailFrom, TrustedEmailsFilePath(filepath.Join(rootDir, "data", "app.sqlite")), appEnv, "http://localhost:8080")

	return NewMultiAlerter(tgAlerter, emAlerter)
}

func E2ETargetPhoneFromEnv() string {
	if targetPhone := os.Getenv("TARGET_PHONE"); targetPhone != "" {
		return targetPhone
	}
	return E2ETargetPhone
}

func NewCampaignHitServer(t *testing.T) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		epg := EPGData{}
		epg.Data.Epg.Title = "BTS"
		epg.Data.Epg.Subtitle = "Dynamite"
		_ = json.NewEncoder(w).Encode(epg)
	}))
}

package poller

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	waTypes "go.mau.fi/whatsmeow/types"
)

func TestKillSwitchAndRadioLogAccessors(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	ctx := context.Background()
	active, err := dbMgr.IsKillSwitchActive(ctx)
	if err != nil {
		t.Fatalf("IsKillSwitchActive() error = %v", err)
	}
	if active {
		t.Fatal("expected kill switch to default to disabled")
	}

	if err := dbMgr.SetKillSwitch(ctx, true); err != nil {
		t.Fatalf("SetKillSwitch(true) error = %v", err)
	}
	active, err = dbMgr.IsKillSwitchActive(ctx)
	if err != nil {
		t.Fatalf("IsKillSwitchActive() error = %v", err)
	}
	if !active {
		t.Fatal("expected kill switch to persist as enabled")
	}

	firstID, err := dbMgr.LogRadioSong(ctx, "BTS", "Dynamite", time.Date(2026, time.June, 27, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LogRadioSong() error = %v", err)
	}
	secondID, err := dbMgr.LogRadioSong(ctx, "Ariana", "7 rings", time.Date(2026, time.June, 27, 12, 2, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LogRadioSong() error = %v", err)
	}

	latestID, err := dbMgr.GetLatestRadioLogID(ctx)
	if err != nil {
		t.Fatalf("GetLatestRadioLogID() error = %v", err)
	}
	if latestID != secondID {
		t.Fatalf("GetLatestRadioLogID() = %d, want %d", latestID, secondID)
	}

	logs, err := dbMgr.GetRadioLogs(ctx, 10)
	if err != nil {
		t.Fatalf("GetRadioLogs() error = %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("len(GetRadioLogs()) = %d, want 2", len(logs))
	}
	if logs[0].ID != int(secondID) || logs[1].ID != int(firstID) {
		t.Fatalf("GetRadioLogs() ids = [%d %d], want [%d %d]", logs[0].ID, logs[1].ID, secondID, firstID)
	}
}

func TestHandleAddSenderPhone(t *testing.T) {
	t.Run("missing phone", func(t *testing.T) {
		server := &TelemetryServer{}
		ctx, rec := newFormContext(http.MethodPost, "/api/sender/add", url.Values{})
		if err := server.handleAddSenderPhone(ctx); err != nil {
			t.Fatalf("handleAddSenderPhone() error = %v", err)
		}
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
		}
	})

	t.Run("missing callback", func(t *testing.T) {
		server := &TelemetryServer{}
		ctx, rec := newFormContext(http.MethodPost, "/api/sender/add", url.Values{"phone": []string{"40700111222"}})
		if err := server.handleAddSenderPhone(ctx); err != nil {
			t.Fatalf("handleAddSenderPhone() error = %v", err)
		}
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	})

	t.Run("success normalizes plus", func(t *testing.T) {
		var got string
		server := &TelemetryServer{}
		server.SetWhatsAppClients([]WhatsAppClient{&MockWhatsAppClient{phone: "+40111222333"}})
		if len(server.wappClients) != 1 {
			t.Fatalf("SetWhatsAppClients() len = %d, want 1", len(server.wappClients))
		}
		server.SetOnAddPhone(func(phone string) error {
			got = phone
			return nil
		})

		ctx, rec := newFormContext(http.MethodPost, "/api/sender/add", url.Values{"phone": []string{"40700111222"}})
		if err := server.handleAddSenderPhone(ctx); err != nil {
			t.Fatalf("handleAddSenderPhone() error = %v", err)
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if got != "+40700111222" {
			t.Fatalf("callback phone = %q, want %q", got, "+40700111222")
		}
	})
}

func TestMockWhatsAppClientBasicMethods(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "mock-wa.sqlite")
	stateMgr := NewStateManager()
	stateMgr.Update(func(s *AppState) {
		s.Connections = []WAConnectionState{{Phone: "+40111222333", Status: StatusPairingRequired}}
	})

	client := &MockWhatsAppClient{phone: "+40111222333", dbPath: dbPath, stateMgr: stateMgr}
	if got := client.AddEventHandler(func(any) {}); got != 1 {
		t.Fatalf("AddEventHandler() = %d, want 1", got)
	}
	if err := client.SendPresence(context.Background(), waTypes.PresenceAvailable); err != nil {
		t.Fatalf("SendPresence() error = %v", err)
	}
	if err := client.SendChatPresence(context.Background(), waTypes.JID{}, waTypes.ChatPresencePaused, waTypes.ChatPresenceMediaAudio); err != nil {
		t.Fatalf("SendChatPresence() error = %v", err)
	}

	client.SimulatePairing()
	if !client.IsConnected() || !client.IsLoggedIn() {
		t.Fatal("expected paired client to be connected and logged in")
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("expected pairing marker at %s: %v", dbPath, err)
	}

	client.Disconnect()
	if client.IsConnected() {
		t.Fatal("expected Disconnect() to clear connected state")
	}
}

func TestSSELogWriterSubscriberBufferHelpers(t *testing.T) {
	broadcaster := NewSSEBroadcaster()
	writer := NewSSELogWriter(&bytes.Buffer{}, broadcaster)

	writer.AddSubscriber()
	if _, err := writer.Write([]byte("one\ntwo\n")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	writer.RemoveSubscriber()

	logs := writer.GetRecentLogs()
	if len(logs) != 2 {
		t.Fatalf("len(GetRecentLogs()) = %d, want 2", len(logs))
	}
	if string(logs[0]) != "one" || string(logs[1]) != "two" {
		t.Fatalf("GetRecentLogs() = %q / %q", string(logs[0]), string(logs[1]))
	}
}

func newFormContext(method string, target string, values url.Values) (*echo.Context, *httptest.ResponseRecorder) {
	e := newTestEcho()
	req := httptest.NewRequest(method, target, bytes.NewBufferString(values.Encode()))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	return ctx, rec
}

func newJSONContext(method string, target string, body []byte) (*echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	return ctx, rec
}

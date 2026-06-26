package poller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestHandleLogoutClearsSessionAndRedirects(t *testing.T) {
	server := &TelemetryServer{}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/logout", nil)
	req.AddCookie(&http.Cookie{
		Name:  "session_token",
		Value: "smoke@example.com",
		Path:  "/",
	})
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := server.handleLogout(ctx); err != nil {
		t.Fatalf("handleLogout() error = %v", err)
	}
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	if location := rec.Header().Get("Location"); location != "/login" {
		t.Fatalf("Location = %q, want /login", location)
	}
	if hxRedirect := rec.Header().Get("HX-Redirect"); hxRedirect != "/login" {
		t.Fatalf("HX-Redirect = %q, want /login", hxRedirect)
	}

	setCookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "session_token=") {
		t.Fatalf("Set-Cookie = %q, want session_token cookie", setCookie)
	}
	if !strings.Contains(setCookie, "Path=/") {
		t.Fatalf("Set-Cookie = %q, want cookie path", setCookie)
	}
}

func TestMockScanRequiresAuth(t *testing.T) {
	t.Setenv("MOCK_WHATSAPP", "true")

	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	dbMgr, err := NewDBManager(dbPath)
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	server := NewTelemetryServer(
		NewAuthManager(dbMgr, "", "", "admin-pass", ""),
		NewStateManager(),
		nil,
		nil,
		dbMgr,
		t.TempDir(),
		t.TempDir(),
		nil,
	)
	server.wappClients = []WhatsAppClient{&MockWhatsAppClient{phone: "+40111222333", dbPath: filepath.Join(t.TempDir(), "mock-wa.sqlite"), stateMgr: server.stateMgr}}

	req := httptest.NewRequest(http.MethodPost, "/api/test/mock-scan", nil)
	rec := httptest.NewRecorder()
	server.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusTemporaryRedirect)
	}
	if location := rec.Header().Get("Location"); location != "/login" {
		t.Fatalf("Location = %q, want /login", location)
	}
}

func TestMockScanPairsAuthenticatedMockClients(t *testing.T) {
	t.Setenv("MOCK_WHATSAPP", "true")

	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "app.sqlite")
	dbMgr, err := NewDBManager(dbPath)
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	trustedEmail := "smoke@example.com"
	if err := os.WriteFile(TrustedEmailsFilePath(dbPath), []byte(trustedEmail+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(trusted emails) error = %v", err)
	}

	stateMgr := NewStateManager()
	mockDBPath := filepath.Join(tempDir, "mock-wa.sqlite")
	server := NewTelemetryServer(
		NewAuthManager(dbMgr, "", "", "admin-pass", ""),
		stateMgr,
		nil,
		nil,
		dbMgr,
		t.TempDir(),
		t.TempDir(),
		nil,
	)
	server.wappClients = []WhatsAppClient{&MockWhatsAppClient{phone: "+40111222333", dbPath: mockDBPath, stateMgr: stateMgr}}
	stateMgr.Update(func(s *AppState) {
		s.Connections = []WAConnectionState{{Phone: "+40111222333", Status: StatusPairingRequired}}
	})

	req := httptest.NewRequest(http.MethodPost, "/api/test/mock-scan", nil)
	req.AddCookie(&http.Cookie{
		Name:  "session_token",
		Value: trustedEmail,
		Path:  "/",
	})
	rec := httptest.NewRecorder()
	server.echo.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !server.wappClients[0].IsLoggedIn() {
		t.Fatal("expected mock client to be logged in after authenticated scan")
	}
	state := stateMgr.Get()
	if len(state.Connections) != 1 || state.Connections[0].Status != StatusConnected || !state.Connections[0].WhatsAppConnected {
		t.Fatalf("state = %+v, want connected sender after scan", state.Connections)
	}
}

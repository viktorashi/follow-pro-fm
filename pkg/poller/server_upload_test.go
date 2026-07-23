package poller

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestHandleAudioUploadRejectsNonOGG(t *testing.T) {
	audiosDir := t.TempDir()
	server := &TelemetryServer{audiosDir: audiosDir}

	ctx, rec := newAudioUploadContext(t, CanonicalSenderPhone, "note.mp3", []byte("fake-mp3"))

	if err := server.handleAudioUpload(ctx); err != nil {
		t.Fatalf("handleAudioUpload returned error: %v", err)
	}

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if body := rec.Body.String(); !strings.Contains(body, "No new valid .ogg files were uploaded") {
		t.Fatalf("body = %q, want .ogg validation message", body)
	}
}

func TestHandleAudioUploadStoresInPerPhonePool(t *testing.T) {
	audiosDir := t.TempDir()
	server := &TelemetryServer{audiosDir: audiosDir}
	phone := "+40111222333"

	ctx, rec := newAudioUploadContext(t, phone, "fresh.ogg", []byte("ogg-data"))

	if err := server.handleAudioUpload(ctx); err != nil {
		t.Fatalf("handleAudioUpload returned error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	activePath := filepath.Join(audiosDir, NormalizePhone(phone), "fresh.ogg")
	usedPath := filepath.Join(audiosDir, NormalizePhone(phone), "used", "fresh.ogg")

	data, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatalf("expected uploaded file at %s: %v", activePath, err)
	}
	if string(data) != "ogg-data" {
		t.Fatalf("uploaded file contents = %q, want %q", string(data), "ogg-data")
	}
	if _, err := os.Stat(usedPath); !os.IsNotExist(err) {
		t.Fatalf("expected no file in used path, stat err = %v", err)
	}
}

func TestHandleAudioUploadStoresCanonicalInRootPool(t *testing.T) {
	audiosDir := t.TempDir()
	server := &TelemetryServer{audiosDir: audiosDir}

	ctx, rec := newAudioUploadContext(t, CanonicalSenderPhone, "canon.ogg", []byte("canon"))

	if err := server.handleAudioUpload(ctx); err != nil {
		t.Fatalf("handleAudioUpload returned error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	activePath := filepath.Join(audiosDir, "canon.ogg")
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("expected canonical upload at %s: %v", activePath, err)
	}
}

func TestHandleAudioUploadRejectsUsedNameCollisions(t *testing.T) {
	audiosDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(audiosDir, "used"), 0755); err != nil {
		t.Fatalf("mkdir used: %v", err)
	}
	if err := os.WriteFile(filepath.Join(audiosDir, "used", "taken.ogg"), []byte("old"), 0644); err != nil {
		t.Fatalf("seed used file: %v", err)
	}

	server := &TelemetryServer{audiosDir: audiosDir}
	ctx, rec := newAudioUploadContext(t, CanonicalSenderPhone, "taken.ogg", []byte("new"))

	if err := server.handleAudioUpload(ctx); err != nil {
		t.Fatalf("handleAudioUpload returned error: %v", err)
	}

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleAudioUploadAllowsPhoneNotPrelistedInDashboardState(t *testing.T) {
	audiosDir := t.TempDir()
	server := &TelemetryServer{
		audiosDir: audiosDir,
		stateMgr:  NewStateManager(),
	}
	server.stateMgr.Update(func(s *AppState) {
		s.Connections = []WAConnectionState{{Phone: "+40111222333", Status: StatusConnected, WhatsAppConnected: true}}
	})

	ctx, rec := newAudioUploadContext(t, "+40999888777", "fresh.ogg", []byte("ogg-data"))

	if err := server.handleAudioUpload(ctx); err != nil {
		t.Fatalf("handleAudioUpload returned error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	activePath := filepath.Join(audiosDir, NormalizePhone("+40999888777"), "fresh.ogg")
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("expected upload at %s: %v", activePath, err)
	}
}

func newAudioUploadContext(t *testing.T, phone string, filename string, contents []byte) (*echo.Context, *httptest.ResponseRecorder) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("phone", phone); err != nil {
		t.Fatalf("WriteField: %v", err)
	}

	part, err := writer.CreateFormFile("audio", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(contents); err != nil {
		t.Fatalf("part.Write: %v", err)
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/audio/upload", &body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	return ctx, rec
}

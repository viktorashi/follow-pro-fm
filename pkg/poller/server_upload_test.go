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

	ctx, rec := newAudioUploadContext(t, "victor-stan", "note.mp3", []byte("fake-mp3"))

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

func TestHandleAudioUploadStoresInPersonPool(t *testing.T) {
	audiosDir := t.TempDir()
	server := &TelemetryServer{audiosDir: audiosDir}
	slug := "victor-stan"

	ctx, rec := newAudioUploadContext(t, slug, "fresh.ogg", []byte("ogg-data"))

	if err := server.handleAudioUpload(ctx); err != nil {
		t.Fatalf("handleAudioUpload returned error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	activePath := filepath.Join(audiosDir, slug, "fresh.ogg")
	usedPath := filepath.Join(audiosDir, slug, "used", "fresh.ogg")

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

func TestHandleAudioUploadRejectsUsedNameCollisions(t *testing.T) {
	audiosDir := t.TempDir()
	slug := "victor-stan"
	personDir := filepath.Join(audiosDir, slug)
	if err := os.MkdirAll(filepath.Join(personDir, "used"), 0755); err != nil {
		t.Fatalf("mkdir used: %v", err)
	}
	if err := os.WriteFile(filepath.Join(personDir, "used", "taken.ogg"), []byte("old"), 0644); err != nil {
		t.Fatalf("seed used file: %v", err)
	}

	server := &TelemetryServer{audiosDir: audiosDir}
	ctx, rec := newAudioUploadContext(t, slug, "taken.ogg", []byte("new"))

	if err := server.handleAudioUpload(ctx); err != nil {
		t.Fatalf("handleAudioUpload returned error: %v", err)
	}

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleAudioUploadWithPersonSlug(t *testing.T) {
	audiosDir := t.TempDir()
	server := &TelemetryServer{
		audiosDir: audiosDir,
		stateMgr:  NewStateManager(),
	}

	ctx, rec := newAudioUploadContext(t, "bubu", "fresh.ogg", []byte("ogg-data"))

	if err := server.handleAudioUpload(ctx); err != nil {
		t.Fatalf("handleAudioUpload returned error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	activePath := filepath.Join(audiosDir, "bubu", "fresh.ogg")
	if _, err := os.Stat(activePath); err != nil {
		t.Fatalf("expected upload at %s: %v", activePath, err)
	}
}

func TestHandleBatchMoveAudios(t *testing.T) {
	audiosDir := t.TempDir()
	fromDir := filepath.Join(audiosDir, "person-a")
	toDir := filepath.Join(audiosDir, "person-b")
	_ = os.MkdirAll(fromDir, 0755)
	_ = os.MkdirAll(toDir, 0755)
	_ = os.WriteFile(filepath.Join(fromDir, "note1.ogg"), []byte("n1"), 0644)
	_ = os.WriteFile(filepath.Join(fromDir, "note2.ogg"), []byte("n2"), 0644)

	server := &TelemetryServer{
		audiosDir: audiosDir,
		stateMgr:  NewStateManager(),
	}

	body := `{"from_slug":"person-a","to_slug":"person-b","files":["note1.ogg","note2.ogg"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/audios/batch-move", strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	e := echo.New()
	ctx := e.NewContext(req, rec)

	if err := server.handleBatchMoveAudios(ctx); err != nil {
		t.Fatalf("handleBatchMoveAudios returned error: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	if _, err := os.Stat(filepath.Join(toDir, "note1.ogg")); err != nil {
		t.Fatalf("expected moved file in toDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fromDir, "note1.ogg")); !os.IsNotExist(err) {
		t.Fatalf("expected source file removed from fromDir: %v", err)
	}
}

func TestHandleAudioPlay(t *testing.T) {
	audiosDir := t.TempDir()
	personDir := filepath.Join(audiosDir, "bubu")
	_ = os.MkdirAll(personDir, 0755)
	_ = os.WriteFile(filepath.Join(personDir, "sample.ogg"), []byte("sample-data"), 0644)

	server := &TelemetryServer{
		audiosDir: audiosDir,
		stateMgr:  NewStateManager(),
	}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/audio/play?slug=bubu&file=sample.ogg", nil)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	if err := server.handleAudioPlay(ctx); err != nil {
		t.Fatalf("handleAudioPlay error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Header().Get("Content-Type") != "audio/ogg" {
		t.Fatalf("content-type = %s, want audio/ogg", rec.Header().Get("Content-Type"))
	}
}

func newAudioUploadContext(t *testing.T, personSlug string, filename string, contents []byte) (*echo.Context, *httptest.ResponseRecorder) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("person_slug", personSlug); err != nil {
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

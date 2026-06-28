package poller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
)

func TestTelemetryServer_UnreviewedChunkEndpointsExposeSavedChunks(t *testing.T) {
	dataDir := t.TempDir()
	unreviewedDir := filepath.Join(dataDir, "signatures", "unreviewed")
	if err := os.MkdirAll(unreviewedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	filename := "BTS - Butter.mp3"
	content := []byte("saved-chunk")
	if err := os.WriteFile(filepath.Join(unreviewedDir, filename), content, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	server := &TelemetryServer{dataDir: dataDir}
	e := echo.New()

	listReq := httptest.NewRequest(http.MethodGet, "/api/unreviewed", nil)
	listRec := httptest.NewRecorder()
	if err := server.handleUnreviewedList(e.NewContext(listReq, listRec)); err != nil {
		t.Fatalf("handleUnreviewedList() error = %v", err)
	}
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d", listRec.Code, http.StatusOK)
	}

	var chunks []ReviewChunk
	if err := json.Unmarshal(listRec.Body.Bytes(), &chunks); err != nil {
		t.Fatalf("Unmarshal(list response) error = %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(chunks))
	}
	if chunks[0].Name != filename {
		t.Fatalf("chunk name = %q, want %q", chunks[0].Name, filename)
	}
	if chunks[0].PlayURL != "/api/unreviewed/file?name=BTS+-+Butter.mp3" {
		t.Fatalf("PlayURL = %q", chunks[0].PlayURL)
	}

	fileReq := httptest.NewRequest(http.MethodGet, "/api/unreviewed/file?name=BTS+-+Butter.mp3", nil)
	fileRec := httptest.NewRecorder()
	if err := server.handleUnreviewedFile(e.NewContext(fileReq, fileRec)); err != nil {
		t.Fatalf("handleUnreviewedFile() error = %v", err)
	}
	if fileRec.Code != http.StatusOK {
		t.Fatalf("file status = %d, want %d", fileRec.Code, http.StatusOK)
	}
	if got := fileRec.Body.String(); got != string(content) {
		t.Fatalf("file body = %q, want %q", got, string(content))
	}
}

func TestTelemetryServer_HandleUnreviewedCropCopiesRecordedMetadataToCanonical(t *testing.T) {
	dataDir := t.TempDir()
	dbMgr := mustNewTestDBManager(t)
	unreviewedDir := filepath.Join(dataDir, "signatures", "unreviewed")
	if err := os.MkdirAll(unreviewedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	filename := "BTS - Butter.mp3"
	if err := os.WriteFile(filepath.Join(unreviewedDir, filename), []byte("abcdefgh"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	recordedAt := bucharestTime(2026, time.June, 17, 12, 0, 0)
	if err := dbMgr.UpsertSignatureFile(context.Background(), "unreviewed", filename, recordedAt, "BTS"); err != nil {
		t.Fatalf("UpsertSignatureFile() error = %v", err)
	}

	server := &TelemetryServer{dataDir: dataDir, dbMgr: dbMgr}
	e := echo.New()

	body := strings.NewReader("filename=BTS+-+Butter.mp3&start_bytes=1&end_bytes=4")
	req := httptest.NewRequest(http.MethodPost, "/unreviewed/crop", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()

	if err := server.handleUnreviewedCrop(e.NewContext(req, rec)); err != nil {
		t.Fatalf("handleUnreviewedCrop() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	meta, err := dbMgr.GetSignatureFile(context.Background(), "canonical", filename)
	if err != nil {
		t.Fatalf("GetSignatureFile(canonical) error = %v", err)
	}
	if meta.CampaignArtist != "BTS" {
		t.Fatalf("CampaignArtist = %q, want %q", meta.CampaignArtist, "BTS")
	}
	if !meta.RecordedAt.Equal(recordedAt) {
		t.Fatalf("RecordedAt = %s, want %s", meta.RecordedAt.Format(time.RFC3339), recordedAt.Format(time.RFC3339))
	}
}

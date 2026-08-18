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
	unreviewedDir := filepath.Join(dataDir, DirSignatures, BucketUnreviewed)
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
	if !strings.HasPrefix(chunks[0].PlayURL, "/api/signatures/file?bucket=unreviewed&name=BTS+-+Butter.mp3&t=") {
		t.Fatalf("PlayURL = %q", chunks[0].PlayURL)
	}

	fileReq := httptest.NewRequest(http.MethodGet, "/api/signatures/file?bucket=unreviewed&name=BTS+-+Butter.mp3", nil)
	fileRec := httptest.NewRecorder()
	if err := server.handleSignatureFile(e.NewContext(fileReq, fileRec)); err != nil {
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
	filename := "BTS - Butter.mp3"
	unreviewedDir := filepath.Join(dataDir, DirSignatures, BucketUnreviewed)
	if err := os.MkdirAll(unreviewedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	audioData, _ := os.ReadFile("testdata/fingerprint/cases/match/stream.mp3")
	if len(audioData) == 0 {
		audioData = []byte("test data") // fallback if testdata is missing
	}
	if err := os.WriteFile(filepath.Join(unreviewedDir, filename), audioData, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	recordedAt := bucharestTime(2026, time.June, 17, 12, 0, 0)
	if err := dbMgr.UpsertSignatureFile(context.Background(), BucketUnreviewed, filename, recordedAt, "BTS", "", ""); err != nil {
		t.Fatalf("UpsertSignatureFile() error = %v", err)
	}

	server := &TelemetryServer{dataDir: dataDir, dbMgr: dbMgr}
	e := echo.New()

	body := strings.NewReader("filename=BTS+-+Butter.mp3&start_seconds=1&end_seconds=4")
	req := httptest.NewRequest(http.MethodPost, "/unreviewed/crop", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()

	if err := server.handleUnreviewedCrop(e.NewContext(req, rec)); err != nil {
		t.Fatalf("handleUnreviewedCrop() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	meta, err := dbMgr.GetSignatureFile(context.Background(), BucketCanonical, filename)
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

func TestTelemetryServer_HandleBatchDeleteUnreviewed(t *testing.T) {
	dataDir := t.TempDir()
	unreviewedDir := filepath.Join(dataDir, DirSignatures, BucketUnreviewed)
	if err := os.MkdirAll(unreviewedDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	files := []string{"chunk1.mp3", "chunk2.mp3", "chunk3.mp3"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(unreviewedDir, f), []byte("dummy audio"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", f, err)
		}
	}

	server := &TelemetryServer{dataDir: dataDir}
	e := echo.New()

	// 1. Test empty request
	reqEmpty := httptest.NewRequest(http.MethodPost, "/api/unreviewed/batch-delete", strings.NewReader(`{"files":[]}`))
	reqEmpty.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recEmpty := httptest.NewRecorder()
	if err := server.handleBatchDeleteUnreviewed(e.NewContext(reqEmpty, recEmpty)); err != nil {
		t.Fatalf("handleBatchDeleteUnreviewed() error = %v", err)
	}
	if recEmpty.Code != http.StatusBadRequest {
		t.Fatalf("empty files status = %d, want %d", recEmpty.Code, http.StatusBadRequest)
	}

	// 2. Test valid batch delete
	payload := `{"files":["chunk1.mp3", "chunk2.mp3", "../unsafe.mp3"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/unreviewed/batch-delete", strings.NewReader(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	if err := server.handleBatchDeleteUnreviewed(e.NewContext(req, rec)); err != nil {
		t.Fatalf("handleBatchDeleteUnreviewed() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var res struct {
		Status  string `json:"status"`
		Deleted int    `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if res.Status != "ok" || res.Deleted != 2 {
		t.Fatalf("res = %+v, want status ok and deleted 2", res)
	}

	if _, err := os.Stat(filepath.Join(unreviewedDir, "chunk1.mp3")); !os.IsNotExist(err) {
		t.Fatalf("chunk1.mp3 still exists")
	}
	if _, err := os.Stat(filepath.Join(unreviewedDir, "chunk2.mp3")); !os.IsNotExist(err) {
		t.Fatalf("chunk2.mp3 still exists")
	}
	if _, err := os.Stat(filepath.Join(unreviewedDir, "chunk3.mp3")); err != nil {
		t.Fatalf("chunk3.mp3 should still exist, got err: %v", err)
	}
}

func TestTelemetryServer_HandleCanonicalDelete(t *testing.T) {
	dataDir := t.TempDir()
	dbMgr := mustNewTestDBManager(t)
	canonicalDir := filepath.Join(dataDir, DirSignatures, BucketCanonical)
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	filename := "The Weeknd - Take My Breath.mp3"
	if err := os.WriteFile(filepath.Join(canonicalDir, filename), []byte("audio-bytes"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := dbMgr.UpsertSignatureFile(context.Background(), BucketCanonical, filename, time.Now(), "The Weeknd", "take my breath", ""); err != nil {
		t.Fatalf("UpsertSignatureFile() error = %v", err)
	}

	server := &TelemetryServer{dataDir: dataDir, dbMgr: dbMgr}
	e := echo.New()

	// 1. Invalid filename
	reqBad := httptest.NewRequest(http.MethodPost, "/canonical/delete", strings.NewReader("filename=../bad.mp3"))
	reqBad.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	recBad := httptest.NewRecorder()
	if err := server.handleCanonicalDelete(e.NewContext(reqBad, recBad)); err != nil {
		t.Fatalf("handleCanonicalDelete(bad) error = %v", err)
	}
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("bad status = %d, want %d", recBad.Code, http.StatusBadRequest)
	}

	// 2. Successful delete
	req := httptest.NewRequest(http.MethodPost, "/canonical/delete", strings.NewReader("filename="+filename))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationForm)
	rec := httptest.NewRecorder()
	if err := server.handleCanonicalDelete(e.NewContext(req, rec)); err != nil {
		t.Fatalf("handleCanonicalDelete() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if _, err := os.Stat(filepath.Join(canonicalDir, filename)); !os.IsNotExist(err) {
		t.Fatalf("file should be removed from disk")
	}
	if _, err := dbMgr.GetSignatureFile(context.Background(), BucketCanonical, filename); err == nil {
		t.Fatalf("signature file should be removed from db")
	}
}

func TestTelemetryServer_HandleBatchDeleteCanonical(t *testing.T) {
	dataDir := t.TempDir()
	dbMgr := mustNewTestDBManager(t)
	canonicalDir := filepath.Join(dataDir, DirSignatures, BucketCanonical)
	if err := os.MkdirAll(canonicalDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	files := []string{"sig1.mp3", "sig2.mp3", "sig3.mp3"}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(canonicalDir, f), []byte("dummy canonical"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", f, err)
		}
		_ = dbMgr.UpsertSignatureFile(context.Background(), BucketCanonical, f, time.Now(), "The Weeknd", "", "")
	}

	server := &TelemetryServer{dataDir: dataDir, dbMgr: dbMgr}
	e := echo.New()

	// 1. Empty files list -> 400
	reqEmpty := httptest.NewRequest(http.MethodPost, "/api/canonical/batch-delete", strings.NewReader(`{"files":[]}`))
	reqEmpty.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recEmpty := httptest.NewRecorder()
	if err := server.handleBatchDeleteCanonical(e.NewContext(reqEmpty, recEmpty)); err != nil {
		t.Fatalf("handleBatchDeleteCanonical(empty) error = %v", err)
	}
	if recEmpty.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recEmpty.Code, http.StatusBadRequest)
	}

	// 2. Batch delete sig1 and sig2
	payload := `{"files":["sig1.mp3", "sig2.mp3", "../traversal.mp3"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/canonical/batch-delete", strings.NewReader(payload))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	if err := server.handleBatchDeleteCanonical(e.NewContext(req, rec)); err != nil {
		t.Fatalf("handleBatchDeleteCanonical() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body: %s", rec.Code, http.StatusOK, rec.Body.String())
	}

	var res struct {
		Status  string `json:"status"`
		Deleted int    `json:"deleted"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if res.Status != "ok" || res.Deleted != 2 {
		t.Fatalf("res = %+v, want ok and deleted=2", res)
	}

	if _, err := os.Stat(filepath.Join(canonicalDir, "sig1.mp3")); !os.IsNotExist(err) {
		t.Fatalf("sig1.mp3 should not exist")
	}
	if _, err := os.Stat(filepath.Join(canonicalDir, "sig2.mp3")); !os.IsNotExist(err) {
		t.Fatalf("sig2.mp3 should not exist")
	}
	if _, err := os.Stat(filepath.Join(canonicalDir, "sig3.mp3")); err != nil {
		t.Fatalf("sig3.mp3 should still exist")
	}
}

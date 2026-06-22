package poller

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

func TestHandleGetScheduleReturnsParsedSchedules(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}
	if err := dbMgr.SetDailySchedule(context.Background(), "2026-06-22", "[1,3,5]"); err != nil {
		t.Fatalf("SetDailySchedule() error = %v", err)
	}

	server := &TelemetryServer{dbMgr: dbMgr}
	ctx, rec := newJSONContext(http.MethodGet, "/api/schedule", nil)

	if err := server.handleGetSchedule(ctx); err != nil {
		t.Fatalf("handleGetSchedule() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.String()
	if !strings.Contains(body, `"2026-06-22":[1,3,5]`) {
		t.Fatalf("response body = %q, want parsed schedule entry", body)
	}
}

func TestHandleSetScheduleAcceptsJSONStringPayload(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	server := &TelemetryServer{dbMgr: dbMgr}
	ctx, rec := newJSONContext(http.MethodPost, "/api/schedule", []byte(`{"date":"2026-06-23","target_matches":"[5,1,3]"}`))

	if err := server.handleSetSchedule(ctx); err != nil {
		t.Fatalf("handleSetSchedule() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	raw, err := dbMgr.GetDailySchedule(context.Background(), "2026-06-23")
	if err != nil {
		t.Fatalf("GetDailySchedule() error = %v", err)
	}
	if raw != "[1,3,5]" {
		t.Fatalf("stored schedule = %q, want %q", raw, "[1,3,5]")
	}
}

func newJSONContext(method string, target string, body []byte) (*echo.Context, *httptest.ResponseRecorder) {
	e := echo.New()
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	return ctx, rec
}

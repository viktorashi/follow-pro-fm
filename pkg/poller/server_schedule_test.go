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

func TestHandleSetScheduleRejectsOutOfRangeMatchIndices(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	server := &TelemetryServer{dbMgr: dbMgr}
	ctx, rec := newJSONContext(http.MethodPost, "/api/schedule", []byte(`{"date":"2026-06-23","target_matches":[0,7]}`))

	if err := server.handleSetSchedule(ctx); err != nil {
		t.Fatalf("handleSetSchedule() error = %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if body := rec.Body.String(); !strings.Contains(body, "1..6") {
		t.Fatalf("body = %q, want range validation error", body)
	}
}

func TestHandleSetScheduleRejectsDateOutsideCampaignWindows(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	server := &TelemetryServer{
		dbMgr: dbMgr,
		campaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
	}
	ctx, rec := newJSONContext(http.MethodPost, "/api/schedule", []byte(`{"date":"2026-06-27","target_matches":[1,3]}`))

	if err := server.handleSetSchedule(ctx); err != nil {
		t.Fatalf("handleSetSchedule() error = %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if body := rec.Body.String(); !strings.Contains(body, "campaign weekday") {
		t.Fatalf("body = %q, want campaign window validation error", body)
	}
}

func TestHandleToggleGatheringPersistsSetting(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	stateMgr := NewStateManager()
	server := &TelemetryServer{dbMgr: dbMgr, stateMgr: stateMgr}
	ctx, rec := newJSONContext(http.MethodPost, "/api/settings/gathering", nil)

	if err := server.handleToggleGathering(ctx); err != nil {
		t.Fatalf("handleToggleGathering() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if stateMgr.Get().GatheringSignatures {
		t.Fatal("expected in-memory state to toggle off")
	}

	active, err := dbMgr.IsGatheringSignaturesEnabled(context.Background())
	if err != nil {
		t.Fatalf("IsGatheringSignaturesEnabled() error = %v", err)
	}
	if active {
		t.Fatal("expected gathering signatures setting to persist as disabled")
	}
}

func TestHandleFillAllSchedulesSetsEveryCampaignWeekdayToAllMatches(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	server := &TelemetryServer{
		dbMgr: dbMgr,
		campaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "17-06-2026", Artist: "BTS"},
			{StartDate: "20-06-2026", EndDate: "23-06-2026", Artist: "Ariana"},
		},
	}
	ctx, rec := newJSONContext(http.MethodPost, "/api/schedule/fill-all", nil)

	if err := server.handleFillAllSchedules(ctx); err != nil {
		t.Fatalf("handleFillAllSchedules() error = %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	schedules, err := dbMgr.GetAllSchedules(context.Background())
	if err != nil {
		t.Fatalf("GetAllSchedules() error = %v", err)
	}

	wantDates := []string{"2026-06-15", "2026-06-16", "2026-06-17", "2026-06-22", "2026-06-23"}
	if len(schedules) != len(wantDates) {
		t.Fatalf("len(schedules) = %d, want %d", len(schedules), len(wantDates))
	}
	for _, date := range wantDates {
		if got := schedules[date]; got != "[1,2,3,4,5,6]" {
			t.Fatalf("schedule[%s] = %q, want %q", date, got, "[1,2,3,4,5,6]")
		}
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

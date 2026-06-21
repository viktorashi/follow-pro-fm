package poller

import (
	"context"
	"testing"
	"time"
)

func TestInitRNGSchedule(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to init db: %v", err)
	}

	campaigns := []Campaign{
		{StartDate: "15-06-2026", EndDate: "16-06-2026", Artist: "TestArtist"},
	}

	err = InitRNGSchedule(dbMgr, campaigns)
	if err != nil {
		t.Fatalf("InitRNGSchedule failed: %v", err)
	}

	ctx := context.Background()

	// 15th is Monday, 16th is Tuesday
	schedules, _ := dbMgr.GetAllSchedules(ctx)
	if len(schedules) != 2 {
		t.Errorf("Expected 2 schedules, got %d", len(schedules))
	}

	// Re-run should not overwrite existing, let's verify it doesn't fail
	err = InitRNGSchedule(dbMgr, campaigns)
	if err != nil {
		t.Errorf("Second InitRNGSchedule failed: %v", err)
	}

	schedules2, _ := dbMgr.GetAllSchedules(ctx)
	if len(schedules2) != 2 {
		t.Errorf("Expected 2 schedules after second init, got %d", len(schedules2))
	}

	// Test IsMatchSelectedToday with dbMgr=nil (should return true by default)
	if !IsMatchSelectedToday(nil, time.Now(), 1) {
		t.Errorf("Expected IsMatchSelectedToday to return true for nil dbMgr")
	}
}

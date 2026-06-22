package poller

import (
	"context"
	"math/rand"
	"os"
	"testing"
	"time"
)

func TestGenerateSchedule(t *testing.T) {
	schedule := generateSchedule(rand.New(rand.NewSource(7)))
	if len(schedule) < minScheduledSendsPerDay || len(schedule) > maxScheduledSendsPerDay {
		t.Fatalf("generateSchedule() length = %d, want between %d and %d", len(schedule), minScheduledSendsPerDay, maxScheduledSendsPerDay)
	}

	seen := make(map[int]struct{}, len(schedule))
	for i, matchIndex := range schedule {
		if matchIndex < 1 || matchIndex > MaxDailyMatches {
			t.Fatalf("generateSchedule() match index = %d, want within 1..%d", matchIndex, MaxDailyMatches)
		}
		if i > 0 && schedule[i-1] >= matchIndex {
			t.Fatalf("generateSchedule() = %v, want strictly increasing order", schedule)
		}
		if _, ok := seen[matchIndex]; ok {
			t.Fatalf("generateSchedule() duplicated match index %d in %v", matchIndex, schedule)
		}
		seen[matchIndex] = struct{}{}
	}
}

func TestInitRNGScheduleBackfillsWithoutOverwrite(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	ctx := context.Background()
	const preservedDate = "2026-06-15"
	const preservedSchedule = "[2,4]"
	if err := dbMgr.SetDailySchedule(ctx, preservedDate, preservedSchedule); err != nil {
		t.Fatalf("SetDailySchedule() error = %v", err)
	}

	campaigns := []Campaign{
		{StartDate: "15-06-2026", EndDate: "17-06-2026", Artist: "BTS"},
	}

	if err := InitRNGSchedule(dbMgr, campaigns); err != nil {
		t.Fatalf("InitRNGSchedule() error = %v", err)
	}

	schedules, err := dbMgr.GetAllSchedules(ctx)
	if err != nil {
		t.Fatalf("GetAllSchedules() error = %v", err)
	}

	if got := schedules[preservedDate]; got != preservedSchedule {
		t.Fatalf("schedule for %s = %s, want preserved %s", preservedDate, got, preservedSchedule)
	}

	if len(schedules) != 3 {
		t.Fatalf("len(schedules) = %d, want 3 weekdays backfilled", len(schedules))
	}

	for _, date := range []string{"2026-06-16", "2026-06-17"} {
		raw, ok := schedules[date]
		if !ok {
			t.Fatalf("expected backfilled schedule for %s", date)
		}
		targetMatches, err := ParseSchedule(raw)
		if err != nil {
			t.Fatalf("ParseSchedule(%s) error = %v", date, err)
		}
		if len(targetMatches) == 0 {
			t.Fatalf("schedule for %s should not be empty", date)
		}
	}
}

func TestIsMatchSelectedToday(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	now := time.Date(2026, time.June, 15, 12, 0, 0, 0, time.UTC)
	if err := dbMgr.SetDailySchedule(context.Background(), now.Format("2006-01-02"), "[2,5]"); err != nil {
		t.Fatalf("SetDailySchedule() error = %v", err)
	}

	selected, err := IsMatchSelectedToday(dbMgr, now, 2)
	if err != nil {
		t.Fatalf("IsMatchSelectedToday() error = %v", err)
	}
	if !selected {
		t.Fatalf("IsMatchSelectedToday() = false, want true for scheduled index")
	}

	selected, err = IsMatchSelectedToday(dbMgr, now, 3)
	if err != nil {
		t.Fatalf("IsMatchSelectedToday() error = %v", err)
	}
	if selected {
		t.Fatalf("IsMatchSelectedToday() = true, want false for unscheduled index")
	}
}

func TestIsMatchSelectedToday_Bypass(t *testing.T) {
	t.Setenv("BYPASS_RNG_SCHEDULE_CHECKS", "true")

	selected, err := IsMatchSelectedToday(nil, time.Now(), MaxDailyMatches)
	if err != nil {
		t.Fatalf("IsMatchSelectedToday() error = %v", err)
	}
	if !selected {
		t.Fatal("IsMatchSelectedToday() = false, want true when bypass is enabled")
	}

	if got := os.Getenv("BYPASS_RNG_SCHEDULE_CHECKS"); got != "true" {
		t.Fatalf("expected bypass env var to remain set during test, got %q", got)
	}
}

func TestNormalizeScheduleJSON(t *testing.T) {
	normalized, err := NormalizeScheduleJSON("[3,1,2]")
	if err != nil {
		t.Fatalf("NormalizeScheduleJSON() error = %v", err)
	}
	if normalized != "[1,2,3]" {
		t.Fatalf("NormalizeScheduleJSON() = %s, want [1,2,3]", normalized)
	}

	if _, err := NormalizeScheduleJSON(`{"bad":true}`); err == nil {
		t.Fatal("NormalizeScheduleJSON() error = nil, want invalid JSON array error")
	}

	if _, err := NormalizeScheduleJSON("[1,1]"); err == nil {
		t.Fatal("NormalizeScheduleJSON() error = nil, want duplicate match index error")
	}

	if _, err := NormalizeScheduleJSON("[0,7]"); err == nil {
		t.Fatal("NormalizeScheduleJSON() error = nil, want out-of-range match index error")
	}
}

func TestIsScheduleDateAllowed(t *testing.T) {
	campaigns := []Campaign{
		{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
	}

	allowed, err := isScheduleDateAllowed("2026-06-23", campaigns, bucharestLocation)
	if err != nil {
		t.Fatalf("isScheduleDateAllowed() error = %v", err)
	}
	if !allowed {
		t.Fatal("isScheduleDateAllowed() = false, want true for in-range campaign weekday")
	}

	allowed, err = isScheduleDateAllowed("2026-06-27", campaigns, bucharestLocation)
	if err != nil {
		t.Fatalf("isScheduleDateAllowed() error = %v", err)
	}
	if allowed {
		t.Fatal("isScheduleDateAllowed() = true, want false for Saturday outside allowed weekdays")
	}
}

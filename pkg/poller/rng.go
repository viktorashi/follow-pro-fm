package poller

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math/rand"
	"os"
	"slices"
	"sort"
	"time"
)

const (
	minScheduledSendsPerDay = 1
	maxScheduledSendsPerDay = 3
)

func InitRNGSchedule(dbMgr *DBManager, campaigns []Campaign) error {
	if dbMgr == nil {
		return nil
	}

	ctx := context.Background()
	existing, err := dbMgr.GetAllSchedules(ctx)
	if err != nil {
		return err
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for _, campaign := range campaigns {
		dates, err := campaignWeekdays(campaign, bucharestLocation)
		if err != nil {
			return err
		}
		for _, day := range dates {
			date := day.Format("2006-01-02")
			if _, ok := existing[date]; ok {
				continue
			}

			scheduleJSON, err := MarshalSchedule(generateSchedule(rng))
			if err != nil {
				return err
			}
			if err := dbMgr.CreateDailyScheduleIfAbsent(ctx, date, scheduleJSON); err != nil {
				return err
			}
			existing[date] = scheduleJSON
		}
	}

	return nil
}

func IsMatchSelectedToday(dbMgr *DBManager, now time.Time, matchIndex int) (bool, error) {
	if shouldBypassRNGSchedule() {
		return true, nil
	}

	if dbMgr == nil {
		return true, nil
	}

	scheduleJSON, err := dbMgr.GetDailySchedule(context.Background(), now.Format("2006-01-02"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return true, nil
		}
		return false, err
	}

	schedule, err := ParseSchedule(scheduleJSON)
	if err != nil {
		return false, err
	}

	return slices.Contains(schedule, matchIndex), nil
}

func ParseSchedule(scheduleJSON string) ([]int, error) {
	var targetMatches []int
	if err := json.Unmarshal([]byte(scheduleJSON), &targetMatches); err != nil {
		return nil, err
	}
	return targetMatches, nil
}

func MarshalSchedule(targetMatches []int) (string, error) {
	normalized := append([]int(nil), targetMatches...)
	sort.Ints(normalized)
	if err := validateScheduleMatches(normalized); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func NormalizeScheduleJSON(scheduleJSON string) (string, error) {
	targetMatches, err := ParseSchedule(scheduleJSON)
	if err != nil {
		return "", err
	}
	return MarshalSchedule(targetMatches)
}

func validateScheduleMatches(targetMatches []int) error {
	seen := make(map[int]struct{}, len(targetMatches))
	for _, matchIndex := range targetMatches {
		if matchIndex < 1 || matchIndex > MaxDailyMatches {
			return errors.New("target_matches must contain unique integers within 1..6")
		}
		if _, ok := seen[matchIndex]; ok {
			return errors.New("target_matches must contain unique integers within 1..6")
		}
		seen[matchIndex] = struct{}{}
	}
	return nil
}

func generateSchedule(rng *rand.Rand) []int {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	count := rng.Intn(maxScheduledSendsPerDay-minScheduledSendsPerDay+1) + minScheduledSendsPerDay
	selected := make(map[int]struct{}, count)
	for len(selected) < count {
		selected[rng.Intn(MaxDailyMatches)+1] = struct{}{}
	}

	schedule := make([]int, 0, len(selected))
	for index := range selected {
		schedule = append(schedule, index)
	}
	sort.Ints(schedule)
	return schedule
}

func campaignWeekdays(campaign Campaign, loc *time.Location) ([]time.Time, error) {
	if loc == nil {
		loc = time.Local
	}

	start, err := time.ParseInLocation("02-01-2006", campaign.StartDate, loc)
	if err != nil {
		return nil, err
	}
	end, err := time.ParseInLocation("02-01-2006", campaign.EndDate, loc)
	if err != nil {
		return nil, err
	}

	var dates []time.Time
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
			continue
		}
		dates = append(dates, day)
	}
	return dates, nil
}

func isScheduleDateAllowed(date string, campaigns []Campaign, loc *time.Location) (bool, error) {
	if loc == nil {
		loc = bucharestLocation
	}

	day, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return false, err
	}
	if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		return false, nil
	}

	for _, campaign := range campaigns {
		start, err := time.ParseInLocation("02-01-2006", campaign.StartDate, loc)
		if err != nil {
			return false, err
		}
		end, err := time.ParseInLocation("02-01-2006", campaign.EndDate, loc)
		if err != nil {
			return false, err
		}
		if !day.Before(start) && !day.After(end) {
			return true, nil
		}
	}

	return false, nil
}

func shouldBypassRNGSchedule() bool {
	return os.Getenv("BYPASS_RNG_SCHEDULE_CHECKS") == "true"
}

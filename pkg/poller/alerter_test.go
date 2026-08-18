package poller

import (
	"context"
	"errors"
	"testing"
)

func TestTelegramAlerter(t *testing.T) {
	// With empty token, it should just return nil and not make HTTP requests
	alerter := NewTelegramAlerter("", "")

	if err := alerter.AlertInfo(AlertEvent{Title: "Info", Message: "test info"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := alerter.AlertSuccess(AlertEvent{Title: "Success", Message: "test success"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := alerter.AlertCritical(AlertEvent{Title: "Critical", Message: "test critical"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEmailAlerter(t *testing.T) {
	alerter := NewEmailAlerter(nil, "from@example.com", "")

	if err := alerter.AlertInfo(AlertEvent{Title: "Info", Message: "test info"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := alerter.AlertSuccess(AlertEvent{Title: "Success", Message: "test success"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := alerter.AlertCritical(AlertEvent{Title: "Critical", Message: "test critical"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestMultiAlerter(t *testing.T) {
	tg := NewTelegramAlerter("", "")
	em := NewEmailAlerter(nil, "", "")

	multi := NewMultiAlerter(tg, em)

	if err := multi.AlertInfo(AlertEvent{Title: "Info", Message: "test info"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := multi.AlertSuccess(AlertEvent{Title: "Success", Message: "test success"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if err := multi.AlertCritical(AlertEvent{Title: "Critical", Message: "test critical"}); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

type failingAlerter struct{ err error }

func (a failingAlerter) AlertCritical(AlertEvent) error { return a.err }
func (a failingAlerter) AlertInfo(AlertEvent) error     { return a.err }
func (a failingAlerter) AlertSuccess(AlertEvent) error  { return a.err }

func TestMultiAlerterDeduplicatesFailuresButNeverContestSongs(t *testing.T) {
	recorder := &recordingAlerter{}
	wantErr := errors.New("delivery failed")
	multi := NewMultiAlerter(failingAlerter{err: wantErr}, recorder)
	disconnect := AlertEvent{Title: "WhatsApp disconnected", Message: "sender 1"}
	if err := multi.AlertCritical(disconnect); !errors.Is(err, wantErr) {
		t.Fatalf("first broadcast error = %v", err)
	}
	if err := multi.AlertCritical(disconnect); err != nil {
		t.Fatalf("deduplicated broadcast error = %v", err)
	}
	if len(recorder.criticalEvents) != 1 {
		t.Fatalf("disconnect deliveries = %d, want 1", len(recorder.criticalEvents))
	}

	contest := AlertEvent{Title: "Contest Song Playing", Message: "BTS"}
	_ = multi.AlertCritical(contest)
	_ = multi.AlertCritical(contest)
	if len(recorder.criticalEvents) != 3 {
		t.Fatalf("contest deliveries were deduplicated: %d total", len(recorder.criticalEvents))
	}
}

func TestDatabaseAlerterPersistsLevels(t *testing.T) {
	dbMgr := mustNewTestDBManager(t)
	alerter := NewDatabaseAlerter(dbMgr)
	event := AlertEvent{Title: "title", Message: "message"}
	if err := alerter.AlertCritical(event); err != nil {
		t.Fatal(err)
	}
	if err := alerter.AlertInfo(event); err != nil {
		t.Fatal(err)
	}
	if err := alerter.AlertSuccess(event); err != nil {
		t.Fatal(err)
	}
	alerts, err := dbMgr.GetRecentAlerts(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SUCCESS", "INFO", "CRITICAL"}
	for i := range want {
		if alerts[i].Level != want[i] || alerts[i].Title != event.Title || alerts[i].Message != event.Message {
			t.Fatalf("alert[%d] = %+v", i, alerts[i])
		}
	}
	if err := NewDatabaseAlerter(nil).AlertInfo(event); err != nil {
		t.Fatalf("disabled database alerter error = %v", err)
	}
}

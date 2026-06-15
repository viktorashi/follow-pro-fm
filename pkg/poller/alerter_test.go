package poller

import (
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
	// With empty api key, it should just return nil
	alerter := NewEmailAlerter("", "from@example.com", "")

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
	em := NewEmailAlerter("", "", "")

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

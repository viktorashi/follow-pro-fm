package poller

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/sendgrid/rest"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func TestTelegramAlerterConfiguredDelivery(t *testing.T) {
	t.Setenv("ENVIRONMENT", "staging")
	t.Setenv("BASE_URL", "https://dashboard.example")
	var payload string
	alerter := NewTelegramAlerter("token", "chat")
	alerter.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.String() != "https://api.telegram.org/bottoken/sendMessage" {
			t.Fatalf("URL = %s", req.URL)
		}
		body, _ := io.ReadAll(req.Body)
		payload = string(body)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})}
	if err := alerter.AlertCritical(AlertEvent{Title: "Disconnected", Message: "sender", ActionURL: "/fix"}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[STAGING]", "Disconnected", "Click Here", "https://dashboard.example"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("Telegram payload missing %q: %s", want, payload)
		}
	}

	alerter.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(bytes.NewReader(nil))}, nil
	})}
	if err := alerter.AlertInfo(AlertEvent{}); err == nil {
		t.Fatal("Telegram HTTP failure returned nil")
	}
	alerter.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("offline")
	})}
	if err := alerter.AlertSuccess(AlertEvent{}); err == nil {
		t.Fatal("Telegram transport failure returned nil")
	}
}

type emailSenderFunc func(*mail.SGMailV3) (*rest.Response, error)

func (fn emailSenderFunc) Send(message *mail.SGMailV3) (*rest.Response, error) { return fn(message) }

func TestEmailAlerterConfiguredDelivery(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("FLY_APP_NAME", "profm")
	targetsFile := t.TempDir() + "/emails.txt"
	if err := os.WriteFile(targetsFile, []byte("first@example.com\nsecond@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var sent *mail.SGMailV3
	alerter := NewEmailAlerter(emailSenderFunc(func(message *mail.SGMailV3) (*rest.Response, error) {
		sent = message
		return &rest.Response{StatusCode: http.StatusAccepted}, nil
	}), "from@example.com", targetsFile)
	event := AlertEvent{Title: "Contest", Message: "line<br><b>bold</b>", ActionLabel: "Open", ActionURL: "/contest"}
	if err := alerter.AlertSuccess(event); err != nil {
		t.Fatal(err)
	}
	if sent == nil || len(sent.Personalizations) != 1 || len(sent.Personalizations[0].To) != 1 || len(sent.Personalizations[0].BCC) != 1 {
		t.Fatalf("email recipients = %#v", sent)
	}
	if !strings.Contains(sent.Content[0].Value, "line\nbold") || !strings.Contains(sent.Content[1].Value, "https://profm.fly.dev") {
		t.Fatalf("email content = %#v", sent.Content)
	}

	alerter.Client = emailSenderFunc(func(*mail.SGMailV3) (*rest.Response, error) {
		return nil, errors.New("offline")
	})
	if err := alerter.AlertInfo(event); err == nil {
		t.Fatal("email transport failure returned nil")
	}
	alerter.Client = emailSenderFunc(func(*mail.SGMailV3) (*rest.Response, error) {
		return &rest.Response{StatusCode: http.StatusBadRequest, Body: "bad request"}, nil
	})
	if err := alerter.AlertCritical(event); err == nil {
		t.Fatal("email HTTP failure returned nil")
	}
}

func TestAlertEnvironmentURLs(t *testing.T) {
	t.Setenv("ENVIRONMENT", "test")
	if got := getEnvPrefix(); got != "[TEST] " {
		t.Fatalf("environment prefix = %q", got)
	}
	t.Setenv("BASE_URL", "")
	t.Setenv("FLY_APP_NAME", "")
	t.Setenv("PORT", "9090")
	if got := getBaseURL(); got != "http://localhost:9090" {
		t.Fatalf("base URL = %q", got)
	}
}

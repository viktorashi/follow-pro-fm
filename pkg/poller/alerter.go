package poller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/resend/resend-go/v3"
)

// Alerter defines the interface for all notification modules.
type Alerter interface {
	AlertCritical(msg string) error
	AlertInfo(msg string) error
	AlertSuccess(msg string) error
}

// MultiAlerter aggregates multiple alerters and sends to all of them.
type MultiAlerter struct {
	alerters []Alerter
}

func NewMultiAlerter(alerters ...Alerter) *MultiAlerter {
	return &MultiAlerter{alerters: alerters}
}

func (m *MultiAlerter) AlertCritical(msg string) error {
	var lastErr error
	for _, a := range m.alerters {
		if err := a.AlertCritical(msg); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (m *MultiAlerter) AlertInfo(msg string) error {
	var lastErr error
	for _, a := range m.alerters {
		if err := a.AlertInfo(msg); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (m *MultiAlerter) AlertSuccess(msg string) error {
	var lastErr error
	for _, a := range m.alerters {
		if err := a.AlertSuccess(msg); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// TelegramAlerter sends notifications via a Telegram Bot.
type TelegramAlerter struct {
	BotToken string
	ChatID   string
}

func NewTelegramAlerter(token, chatID string) *TelegramAlerter {
	return &TelegramAlerter{BotToken: token, ChatID: chatID}
}

func (t *TelegramAlerter) send(prefix, msg string) error {
	if t.BotToken == "" || t.ChatID == "" {
		return nil // Disabled if not configured
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.BotToken)
	payload := map[string]string{
		"chat_id": t.ChatID,
		"text":    prefix + " " + msg,
	}
	body, _ := json.Marshal(payload)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("telegram API error: status %d", resp.StatusCode)
	}
	return nil
}

func (t *TelegramAlerter) AlertCritical(msg string) error {
	return t.send("🚨 [CRITICAL]", msg)
}

func (t *TelegramAlerter) AlertInfo(msg string) error {
	return t.send("ℹ️ [INFO]", msg)
}

func (t *TelegramAlerter) AlertSuccess(msg string) error {
	return t.send("✅ [SUCCESS]", msg)
}

// EmailAlerter sends notifications via Resend API.
type EmailAlerter struct {
	Client      *resend.Client
	FromEmail   string
	TargetsFile string // Path to file containing trusted emails
}

func NewEmailAlerter(apiKey string, from string, targetsFile string) *EmailAlerter {
	if apiKey == "" {
		return &EmailAlerter{} // Disabled
	}
	return &EmailAlerter{
		Client:      resend.NewClient(apiKey),
		FromEmail:   from,
		TargetsFile: targetsFile,
	}
}

func (e *EmailAlerter) send(prefix, msg string) error {
	if e.Client == nil || e.TargetsFile == "" {
		return nil
	}

	var targets []string
	if data, err := os.ReadFile(e.TargetsFile); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			email := strings.TrimSpace(line)
			if email != "" {
				targets = append(targets, email)
			}
		}
	}

	if len(targets) == 0 {
		return nil // No one to email
	}

	params := &resend.SendEmailRequest{
		From:    e.FromEmail,
		To:      targets,
		Subject: prefix + " ProFM Poller Alert",
		Html:    fmt.Sprintf("<p>%s</p>", msg),
	}

	_, err := e.Client.Emails.Send(params)
	return err
}

func (e *EmailAlerter) AlertCritical(msg string) error {
	return e.send("🚨 CRITICAL:", msg)
}

func (e *EmailAlerter) AlertInfo(msg string) error {
	return e.send("ℹ️ INFO:", msg)
}

func (e *EmailAlerter) AlertSuccess(msg string) error {
	return e.send("✅ SUCCESS:", msg)
}

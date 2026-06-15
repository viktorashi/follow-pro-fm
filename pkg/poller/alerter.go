package poller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/sendgrid/sendgrid-go"
	"github.com/sendgrid/sendgrid-go/helpers/mail"
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
	return &TelegramAlerter{
		BotToken: token,
		ChatID:   chatID,
	}
}

func (t *TelegramAlerter) send(prefix, msg string) error {
	if t.BotToken == "" || t.ChatID == "" {
		return nil // Disabled if not configured
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.BotToken)
	payload := map[string]string{
		"chat_id":    t.ChatID,
		"text":       prefix + " " + msg,
		"parse_mode": "HTML",
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

// EmailAlerter sends notifications via SendGrid API.
type EmailAlerter struct {
	Client      *sendgrid.Client
	FromEmail   string
	TargetsFile string // Path to file containing trusted emails
}

func NewEmailAlerter(apiKey string, from string, targetsFile string) *EmailAlerter {
	if apiKey == "" {
		return &EmailAlerter{} // Disabled
	}
	client := sendgrid.NewSendClient(apiKey)

	return &EmailAlerter{
		Client:      client,
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

	from := mail.NewEmail("ProFM Poller", e.FromEmail)
	subject := prefix + " ProFM Poller Alert"
	htmlContent := fmt.Sprintf("<p>%s</p>", strings.ReplaceAll(msg, "\n", "<br>"))

	// SendGrid uses personalizations for multiple BCC/To
	m := mail.NewV3Mail()
	m.SetFrom(from)
	m.Subject = subject

	p := mail.NewPersonalization()
	for i, t := range targets {
		to := mail.NewEmail("", t)
		if i == 0 {
			p.AddTos(to)
		} else {
			p.AddBCCs(to)
		}
	}
	m.AddPersonalizations(p)

	content := mail.NewContent("text/html", htmlContent)
	m.AddContent(content)

	response, err := e.Client.Send(m)
	if err != nil {
		return fmt.Errorf("sendgrid send error: %w", err)
	}

	if response.StatusCode >= 400 {
		return fmt.Errorf("sendgrid returned status %d: %s", response.StatusCode, response.Body)
	}

	return nil
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

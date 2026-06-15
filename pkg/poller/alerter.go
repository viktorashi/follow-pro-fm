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

// AlertEvent contains structured data for an alert.
type AlertEvent struct {
	Title       string
	Message     string
	ActionLabel string
	ActionURL   string
}

// Alerter defines the interface for all notification modules.
type Alerter interface {
	AlertCritical(event AlertEvent) error
	AlertInfo(event AlertEvent) error
	AlertSuccess(event AlertEvent) error
}

// MultiAlerter aggregates multiple alerters and sends to all of them.
type MultiAlerter struct {
	alerters []Alerter
}

func NewMultiAlerter(alerters ...Alerter) *MultiAlerter {
	return &MultiAlerter{alerters: alerters}
}

func (m *MultiAlerter) AlertCritical(event AlertEvent) error {
	var lastErr error
	for _, a := range m.alerters {
		if err := a.AlertCritical(event); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (m *MultiAlerter) AlertInfo(event AlertEvent) error {
	var lastErr error
	for _, a := range m.alerters {
		if err := a.AlertInfo(event); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (m *MultiAlerter) AlertSuccess(event AlertEvent) error {
	var lastErr error
	for _, a := range m.alerters {
		if err := a.AlertSuccess(event); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// TelegramAlerter sends notifications via a Telegram Bot.
type TelegramAlerter struct {
	BotToken string
	ChatID   string
	Env      string
	BaseURL  string
}

func NewTelegramAlerter(token, chatID, env, baseURL string) *TelegramAlerter {
	return &TelegramAlerter{
		BotToken: token,
		ChatID:   chatID,
		Env:      env,
		BaseURL:  baseURL,
	}
}

func (t *TelegramAlerter) send(prefix string, event AlertEvent) error {
	if t.BotToken == "" || t.ChatID == "" {
		return nil // Disabled if not configured
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.BotToken)

	envPrefix := ""
	if t.Env != "prod" && t.Env != "production" && t.Env != "" {
		envPrefix = fmt.Sprintf("[%s] ", strings.ToUpper(t.Env))
	}

	// Format Telegram message
	msg := fmt.Sprintf("<b>%s%s %s</b>\n\n%s", envPrefix, prefix, event.Title, event.Message)
	if event.ActionURL != "" {
		label := event.ActionLabel
		if label == "" {
			label = "Click Here"
		}
		msg += fmt.Sprintf("\n\n<a href=\"%s\">%s</a>", event.ActionURL, label)
	}

	if t.BaseURL != "" && event.ActionURL != t.BaseURL {
		msg += fmt.Sprintf("\n\n🌐 Live Dashboard: %s", t.BaseURL)
	}

	payload := map[string]string{
		"chat_id":    t.ChatID,
		"text":       msg,
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

func (t *TelegramAlerter) AlertCritical(event AlertEvent) error {
	return t.send("🚨 [CRITICAL]", event)
}

func (t *TelegramAlerter) AlertInfo(event AlertEvent) error {
	return t.send("ℹ️ [INFO]", event)
}

func (t *TelegramAlerter) AlertSuccess(event AlertEvent) error {
	return t.send("✅ [SUCCESS]", event)
}

// EmailAlerter sends notifications via SendGrid API.
type EmailAlerter struct {
	Client      *sendgrid.Client
	FromEmail   string
	TargetsFile string // Path to file containing trusted emails
	Env         string
	BaseURL     string
}

func NewEmailAlerter(apiKey string, from string, targetsFile string, env string, baseURL string) *EmailAlerter {
	if apiKey == "" {
		return &EmailAlerter{} // Disabled
	}
	client := sendgrid.NewSendClient(apiKey)

	return &EmailAlerter{
		Client:      client,
		FromEmail:   from,
		TargetsFile: targetsFile,
		Env:         env,
		BaseURL:     baseURL,
	}
}

func (e *EmailAlerter) send(prefix string, event AlertEvent) error {
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

	envPrefix := ""
	if e.Env != "prod" && e.Env != "production" && e.Env != "" {
		envPrefix = fmt.Sprintf("[%s] ", strings.ToUpper(e.Env))
	}

	from := mail.NewEmail("ProFM Poller", e.FromEmail)
	subject := envPrefix + prefix + " " + event.Title

	// Create a plain text version of the HTML message
	plainTextContent := strings.ReplaceAll(event.Message, "<br>", "\n")
	for {
		start := strings.Index(plainTextContent, "<")
		if start == -1 {
			break
		}
		end := strings.Index(plainTextContent[start:], ">")
		if end == -1 {
			break
		}
		plainTextContent = plainTextContent[:start] + plainTextContent[start+end+1:]
	}

	if event.ActionURL != "" {
		plainTextContent += fmt.Sprintf("\n\n%s: %s", event.ActionLabel, event.ActionURL)
	}

	htmlContent := fmt.Sprintf("<p>%s</p>", strings.ReplaceAll(event.Message, "\n", "<br>"))
	if event.ActionURL != "" {
		label := event.ActionLabel
		if label == "" {
			label = "Click Here"
		}
		htmlContent += fmt.Sprintf("<br><br><a href=\"%s\" style=\"padding: 10px 20px; background-color: #007bff; color: white; text-decoration: none; border-radius: 5px;\">%s</a>", event.ActionURL, label)
	}

	if e.BaseURL != "" && event.ActionURL != e.BaseURL {
		plainTextContent += fmt.Sprintf("\n\nLive Dashboard: %s", e.BaseURL)
		htmlContent += fmt.Sprintf("<br><br>🌐 Live Dashboard: <a href=\"%s\">%s</a>", e.BaseURL, e.BaseURL)
	}

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

	m.AddContent(mail.NewContent("text/plain", plainTextContent))
	m.AddContent(mail.NewContent("text/html", htmlContent))

	response, err := e.Client.Send(m)
	if err != nil {
		return fmt.Errorf("sendgrid send error: %w", err)
	}

	if response.StatusCode >= 400 {
		return fmt.Errorf("sendgrid returned status %d: %s", response.StatusCode, response.Body)
	}

	return nil
}

func (e *EmailAlerter) AlertCritical(event AlertEvent) error {
	return e.send("🚨 CRITICAL:", event)
}

func (e *EmailAlerter) AlertInfo(event AlertEvent) error {
	return e.send("ℹ️ INFO:", event)
}

func (e *EmailAlerter) AlertSuccess(event AlertEvent) error {
	return e.send("✅ SUCCESS:", event)
}

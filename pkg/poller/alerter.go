package poller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/sendgrid/sendgrid-go/helpers/mail"
)

func getEnvPrefix() string {
	envName := os.Getenv("ENVIRONMENT")
	if envName == "" {
		envName = "production"
	}
	if envName != "prod" && envName != "production" {
		return fmt.Sprintf("[%s] ", strings.ToUpper(envName))
	}
	return ""
}

func getBaseURL() string {
	baseURL := os.Getenv("BASE_URL")
	if baseURL != "" {
		return baseURL
	}
	if appName := os.Getenv("FLY_APP_NAME"); appName != "" {
		return fmt.Sprintf("https://%s.fly.dev", appName)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	return "http://localhost:" + port
}

// AlertEvent contains structured data for an alert.
type AlertEvent struct {
	Title       string
	Message     string
	ActionLabel string
	ActionURL   string
}

func (event AlertEvent) actionLabel() string {
	if event.ActionLabel != "" {
		return event.ActionLabel
	}
	return "Click Here"
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
	lastSent map[string]time.Time
	mu       sync.Mutex
}

func NewMultiAlerter(alerters ...Alerter) *MultiAlerter {
	return &MultiAlerter{
		alerters: alerters,
		lastSent: make(map[string]time.Time),
	}
}

func (m *MultiAlerter) shouldSend(event AlertEvent) bool {
	if event.Title == "Contest Song Playing" {
		return true // Always send contest song alerts without deduplication
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := event.Title + "|" + event.Message
	if last, exists := m.lastSent[key]; exists {
		// Suppress identical alerts (e.g. WhatsApp disconnects) for 4 hours
		if time.Since(last) < 4*time.Hour {
			return false
		}
	}
	m.lastSent[key] = time.Now()
	return true
}

func (m *MultiAlerter) broadcast(event AlertEvent, sender func(Alerter, AlertEvent) error) error {
	if !m.shouldSend(event) {
		return nil
	}
	var lastErr error
	for _, a := range m.alerters {
		if err := sender(a, event); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (m *MultiAlerter) AlertCritical(event AlertEvent) error {
	return m.broadcast(event, Alerter.AlertCritical)
}

func (m *MultiAlerter) AlertInfo(event AlertEvent) error {
	return m.broadcast(event, Alerter.AlertInfo)
}

func (m *MultiAlerter) AlertSuccess(event AlertEvent) error {
	return m.broadcast(event, Alerter.AlertSuccess)
}

// TelegramAlerter sends notifications via a Telegram Bot.
type TelegramAlerter struct {
	BotToken string
	ChatID   string
	Client   *http.Client
}

func NewTelegramAlerter(token, chatID string) *TelegramAlerter {
	return &TelegramAlerter{
		BotToken: token,
		ChatID:   chatID,
		Client:   http.DefaultClient,
	}
}

func (t *TelegramAlerter) send(prefix string, event AlertEvent) error {
	if t.BotToken == "" || t.ChatID == "" {
		return nil // Disabled if not configured
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", t.BotToken)

	// Format Telegram message
	msg := fmt.Sprintf("<b>%s%s %s</b>\n\n%s", getEnvPrefix(), prefix, event.Title, event.Message)
	if event.ActionURL != "" {
		msg += fmt.Sprintf("\n\n<a href=\"%s\">%s</a>", event.ActionURL, event.actionLabel())
	}

	baseURL := getBaseURL()

	if baseURL != "" && event.ActionURL != baseURL {
		msg += fmt.Sprintf("\n\n<a href=\"%s\">Live Dashboard</a>", baseURL)
	}

	payload := map[string]string{
		"chat_id":    t.ChatID,
		"text":       msg,
		"parse_mode": "HTML",
	}
	body, _ := json.Marshal(payload)

	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(body))
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
	Client      EmailSender
	FromEmail   string
	TargetsFile string // Path to file containing trusted emails
}

func NewEmailAlerter(client EmailSender, from string, targetsFile string) *EmailAlerter {
	return &EmailAlerter{
		Client:      client,
		FromEmail:   from,
		TargetsFile: targetsFile,
	}
}

func (e *EmailAlerter) send(prefix string, event AlertEvent) error {
	if e.Client == nil || e.TargetsFile == "" {
		return nil
	}

	var targets []string
	if data, err := os.ReadFile(e.TargetsFile); err == nil {
		for line := range strings.SplitSeq(string(data), "\n") {
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
	subject := getEnvPrefix() + prefix + " " + event.Title

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
		plainTextContent += fmt.Sprintf("\n\n%s: %s", event.actionLabel(), event.ActionURL)
	}

	htmlContent := fmt.Sprintf("<p>%s</p>", strings.ReplaceAll(event.Message, "\n", "<br>"))
	if event.ActionURL != "" {
		htmlContent += fmt.Sprintf("<br><br><a href=\"%s\" style=\"padding: 10px 20px; background-color: #007bff; color: white; text-decoration: none; border-radius: 5px;\">%s</a>", event.ActionURL, event.actionLabel())
	}

	baseURL := getBaseURL()

	if baseURL != "" && event.ActionURL != baseURL {
		plainTextContent += fmt.Sprintf("\n\nLive Dashboard: %s", baseURL)
		htmlContent += fmt.Sprintf("<br><br><a href=\"%s\" style=\"padding: 10px 20px; background-color: #28a745; color: white; text-decoration: none; border-radius: 5px;\">Live Dashboard</a>", baseURL)
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

// DatabaseAlerter saves alerts to the application database.
type DatabaseAlerter struct {
	dbMgr *DBManager
}

func NewDatabaseAlerter(dbMgr *DBManager) *DatabaseAlerter {
	return &DatabaseAlerter{
		dbMgr: dbMgr,
	}
}

func (d *DatabaseAlerter) save(level string, event AlertEvent) error {
	if d.dbMgr == nil {
		return nil
	}
	// Best-effort save: use a short timeout so alerting can't block the caller if the DB is locked.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return d.dbMgr.SaveAlert(ctx, level, event.Title, event.Message, time.Now().UTC())
}

func (d *DatabaseAlerter) AlertCritical(event AlertEvent) error {
	return d.save("CRITICAL", event)
}

func (d *DatabaseAlerter) AlertInfo(event AlertEvent) error {
	return d.save("INFO", event)
}

func (d *DatabaseAlerter) AlertSuccess(event AlertEvent) error {
	return d.save("SUCCESS", event)
}

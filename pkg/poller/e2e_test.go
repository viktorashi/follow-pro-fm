//go:build e2e
// +build e2e

package poller

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"os"
)

func TestDashboardUploadE2E(t *testing.T) {
	// Initialize the Server
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to init DB: %v", err)
	}
	stateMgr := NewStateManager()

	authMgr := NewAuthManager(dbMgr, "", "", "admin", "")
	broadcaster := NewSSEBroadcaster()
	logWriter := NewSSELogWriter(nil, broadcaster)
	server := NewTelemetryServer(authMgr, stateMgr, broadcaster, logWriter, dbMgr, "test_data")

	// Start local test server
	ts := httptest.NewServer(server.echo)
	defer ts.Close()

	// Initialize Playwright
	pw, err := playwright.Run()
	if err != nil {
		t.Fatalf("could not start playwright: %v", err)
	}
	defer pw.Stop()

	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		t.Fatalf("could not launch browser: %v", err)
	}
	defer browser.Close()

	page, err := browser.NewPage()
	if err != nil {
		t.Fatalf("could not create page: %v", err)
	}

	// Login
	if _, err := page.Goto(ts.URL + "/login"); err != nil {
		t.Fatalf("could not goto login: %v", err)
	}

	os.MkdirAll("test_data", 0755)
	defer os.RemoveAll("test_data")

	// Add trusted email
	os.WriteFile("trusted-emails.txt", []byte("test@test.com"), 0o644)
	defer os.Remove("trusted-emails.txt")

	if err := page.Fill("input[name=email]", "test@test.com"); err != nil {
		t.Fatalf("could not fill email: %v", err)
	}
	if err := page.Fill("input[name=password]", "admin"); err != nil {
		t.Fatalf("could not fill password: %v", err)
	}
	if err := page.Click("button[type=submit]"); err != nil {
		t.Fatalf("could not click login: %v", err)
	}

	// Wait for navigation
	page.WaitForURL(ts.URL + "/")

	// Navigate to Files page
	if err := page.Click("a:has-text('Files')"); err != nil {
		t.Fatalf("could not click Files: %v", err)
	}
	page.WaitForURL(ts.URL + "/data")

	// Wait for Data page to load by checking for a known element
	if _, err := page.WaitForSelector("h2:has-text('Upload Voice Note')", playwright.PageWaitForSelectorOptions{Timeout: playwright.Float(5000)}); err != nil {
		t.Fatalf("could not find upload section: %v", err)
	}

	// Create a dummy audio file to upload
	dummyAudioPath := "test_data/dummy.ogg"
	os.WriteFile(dummyAudioPath, []byte("dummy audio content"), 0644)

	// Fill upload form
	if err := page.Fill("input[name=phone]", "+40 123 456 789"); err != nil {
		t.Fatalf("could not fill phone: %v", err)
	}

	// Set file input
	if err := page.SetInputFiles("input[id=audio-upload]", playwright.InputFile{
		Name:     "dummy.ogg",
		MimeType: "audio/ogg",
		Buffer:   []byte("dummy audio content"),
	}); err != nil {
		t.Fatalf("could not set input file: %v", err)
	}

	// Submit the upload form
	if err := page.Click("button:has-text('Upload Voice Note')"); err != nil {
		t.Fatalf("could not click upload button: %v", err)
	}

	// Wait a moment for upload to complete
	time.Sleep(1 * time.Second)

	// Check if file was uploaded to the correct directory
	// Expected path: test_data/phone_+40 123 456 789/dummy.ogg
	uploadedPath := "test_data/+40 123 456 789/used/dummy.ogg"
	if _, err := os.Stat(uploadedPath); os.IsNotExist(err) {
		t.Fatalf("Uploaded file not found at %s", uploadedPath)
	}

	t.Log("Test passed - File uploaded successfully")
}

//go:build e2e
// +build e2e

package main

import (
	"testing"
)

func TestPoller_E2E(t *testing.T) {
	t.Skip("E2E test requires physical device pairing. Run manually by removing this skip or passing a specific target phone.")
	
	// Example of what an E2E test would look like:
	// 1. Initialize DB and WhatsApp client (this will prompt for QR code if not already linked)
	// 2. Set TargetPhone to a test burner number
	// 3. Spin up the Poller, mock the HTTP server to force a song match
	// 4. Verify physically that the burner phone received the Voice Note.
}

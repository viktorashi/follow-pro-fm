//go:build e2e && !nowapp
// +build e2e,!nowapp

package poller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPoller_E2E(t *testing.T) {
	rootDir := E2EProjectRoot(t)
	dbPath := filepath.Join(rootDir, "data/wapp.sqlite")
	appDBPath := filepath.Join(rootDir, "data/app.sqlite")
	e2eDBPath := filepath.Join(rootDir, "data/app_e2e_test.sqlite")

	// Copy the real app.sqlite to a test-specific file so we retain the WhatsApp sessions,
	// but we don't accidentally wipe the user's real local radio_log and campaign state!
	if data, err := os.ReadFile(appDBPath); err == nil {
		_ = os.WriteFile(e2eDBPath, data, 0644)
	} else {
		// fallback to just making an empty one if it doesn't exist
		_ = os.WriteFile(e2eDBPath, []byte(""), 0644)
	}
	defer os.Remove(e2eDBPath) // Cleanup after test!

	audiosDir := filepath.Join(rootDir, "data/audios")
	LoadE2EEnv(rootDir)
	multiAlerter := E2EMultiAlerter(rootDir)

	stateMgr := NewStateManager()

	ensureConn := func(p string) {
		stateMgr.Update(func(s *AppState) {
			found := false
			for _, conn := range s.Connections {
				if conn.Phone == p {
					found = true
					break
				}
			}
			if !found {
				s.Connections = append(s.Connections, WAConnectionState{
					Phone:             p,
					Status:            StatusConnected,
					WhatsAppConnected: true,
				})
			}
		})
	}

	t.Log("Initializing real WhatsApp clients...")

	wappClients := make(map[string]WhatsAppClient)

	// Add Canonical
	canonical, err := InitWhatsApp("+40734788254", dbPath, stateMgr, multiAlerter, "")
	if err == nil {
		wappClients["+40734788254"] = canonical
		ensureConn("+40734788254")
	}

	// Add others from DB
	dbMgr, err := NewDBManager(e2eDBPath)
	if err == nil {
		sessions, err := dbMgr.SenderSessions(context.Background())
		if err == nil {
			for _, session := range sessions {
				if _, exists := wappClients[session.Phone]; !exists {
					dbForPhone := filepath.Join(filepath.Dir(dbPath), session.DBFilename)
					client, err := InitWhatsApp(session.Phone, dbForPhone, stateMgr, multiAlerter, "")
					if err == nil {
						wappClients[session.Phone] = client
						ensureConn(session.Phone)
					}
				}
			}
		}
	}

	defer func() {
		for _, c := range wappClients {
			c.Disconnect()
		}
	}()

	// Wipe tables to ensure clean E2E run
	if dbMgr != nil {
		_, _ = dbMgr.db.Exec("DELETE FROM radio_log")
		_, _ = dbMgr.db.Exec("DELETE FROM played_songs")
		_, _ = dbMgr.db.Exec("DELETE FROM used_audio_hashes")
		_, _ = dbMgr.db.Exec("DELETE FROM campaign_send_state")
	}

	targetPhone := E2ETargetPhoneFromEnv()

	activeTime := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)

	server := NewCampaignHitServer(t)
	defer server.Close()

	var sentCount int
	poller := &Poller{
		APIURL:       server.URL,
		PollInterval: 1 * time.Millisecond,
		ActiveCampaigns: []Campaign{
			{StartDate: "15-06-2026", EndDate: "26-06-2026", Artist: "BTS"},
		},
		TargetPhone: targetPhone,
		StateMgr:    stateMgr,
		Alerter:     multiAlerter,
		AudiosDir:   audiosDir,
		DBMgr:       dbMgr,
		SendVoiceNote: func(senderPhone string, targetPhone string, audioPath string) error {
			client, ok := wappClients[senderPhone]
			if !ok {
				t.Fatalf("Sender phone %s not found in initialized clients", senderPhone)
			}
			t.Logf("🚀 Triggering real E2E voice note send from %s to %s...", senderPhone, targetPhone)
			sentCount++
			return SendVoiceNote(client, targetPhone, audioPath)
		},
	}

	currentSong := &SongInfo{}

	t.Log("Triggering song check...")
	poller.checkSong(currentSong, activeTime)

	if poller.matchesToday != 1 {
		t.Fatalf("Expected 1 match to trigger message, got %d", poller.matchesToday)
	}

	t.Logf("✅ E2E Test finished successfully. Sent %d voice notes across %d connected clients.", sentCount, len(wappClients))
}

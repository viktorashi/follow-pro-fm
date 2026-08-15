//go:build e2e && !nowapp
// +build e2e,!nowapp

package poller

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPoller_E2E(t *testing.T) {
	rootDir := E2EProjectRoot(t)
	appDBPath := filepath.Join(rootDir, "data/app.sqlite")

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

	dbPath := filepath.Join(rootDir, "data/wapp.sqlite")

	t.Log("Initializing real WhatsApp clients...")

	wappClients := make(map[string]WhatsAppClient)

	// Add Canonical
	canonical, err := InitWhatsApp("+40734788254", dbPath, stateMgr, multiAlerter, "")
	if err == nil {
		wappClients["+40734788254"] = canonical
		ensureConn("+40734788254")
	}

	// Make an in-memory DB for the rest of the app state for blazing fast tests
	dbMgr, err := NewDBManager(":memory:")

	// Open the physical DB exclusively to harvest the WhatsApp session paths
	importDbMgr, err2 := NewDBManager(appDBPath)
	if err == nil && err2 == nil {
		sessions, err := importDbMgr.SenderSessions(context.Background())
		if err == nil {
			for _, session := range sessions {
				_, _ = dbMgr.db.Exec("INSERT INTO sender_sessions (phone, db_filename) VALUES (?, ?)", session.Phone, session.DBFilename)
			}
		}
	}
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
	(&metadataContestChecker{poller: poller, coordinator: poller.contestCheckCoordinator(), currentSong: currentSong}).Check(activeTime)

	if poller.matchesToday != 1 {
		t.Fatalf("Expected 1 match to trigger message, got %d", poller.matchesToday)
	}

	t.Logf("✅ E2E Test finished successfully. Sent %d voice notes across %d connected clients.", sentCount, len(wappClients))
}

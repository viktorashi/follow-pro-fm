//go:build e2e
// +build e2e

package poller

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPoller_E2E_NoWhatsApp(t *testing.T) {
	rootDir := E2EProjectRoot(t)
	audiosDir := filepath.Join(rootDir, "data/audios")
	appDBPath := filepath.Join(rootDir, "data/app.sqlite")
	LoadE2EEnv(rootDir)

	t.Log("Skipping real WhatsApp client initialization (nowapp build tag)")

	if err := InitAudioPool(audiosDir); err != nil {
		t.Fatalf("Failed to initialize audio pool: %v", err)
	}

	multiAlerter := E2EMultiAlerter(rootDir)
	targetPhone := E2ETargetPhoneFromEnv()

	activeTime := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)

	server := NewCampaignHitServer(t)
	defer server.Close()

	stateMgr := NewStateManager()

	// Make an in-memory DB and import persons + sessions from the real DB
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to create in-memory DB: %v", err)
	}
	importDbMgr, err := NewDBManager(appDBPath)
	if err == nil {
		persons, persErr := importDbMgr.ListPersons(context.Background())
		if persErr == nil {
			for _, p := range persons {
				_, _ = dbMgr.db.Exec("INSERT INTO persons (id, name, slug, created_at) VALUES (?, ?, ?, ?)", p.ID, p.Name, p.Slug, p.CreatedAt)
			}
		}
		sessions, sessErr := importDbMgr.SenderSessions(context.Background())
		if sessErr == nil {
			for _, session := range sessions {
				_, _ = dbMgr.db.Exec("INSERT INTO sender_sessions (phone, db_filename, person_id) VALUES (?, ?, ?)", session.Phone, session.DBFilename, session.PersonID)
			}
		}
	}

	// Create connections from imported sessions
	sessions, _ := dbMgr.SenderSessions(context.Background())
	stateMgr.Update(func(s *AppState) {
		for _, sess := range sessions {
			s.Connections = append(s.Connections, WAConnectionState{
				Phone:             sess.Phone,
				WhatsAppConnected: true,
				Status:            StatusConnected,
			})
		}
	})
	HydrateConnectionPersons(dbMgr, stateMgr, context.Background())

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
			t.Logf("🚀 Simulating voice note send from %s to %s (audio: %s)", senderPhone, targetPhone, audioPath)
			return nil
		},
	}

	currentSong := &SongInfo{}

	t.Log("Triggering song check...")
	(&metadataContestChecker{poller: poller, coordinator: poller.contestCheckCoordinator(), currentSong: currentSong}).Check(activeTime)

	if poller.matchesToday != 1 {
		t.Fatalf("Expected 1 match to trigger message, got %d", poller.matchesToday)
	}

	t.Log("E2E test complete! Check your phone for the voice note.")
}

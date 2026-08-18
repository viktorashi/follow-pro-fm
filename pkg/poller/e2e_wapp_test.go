//go:build e2e && wapp
// +build e2e,wapp

package poller

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestPoller_E2E(t *testing.T) {
	rootDir := E2EProjectRoot(t)
	dataDir := filepath.Join(rootDir, "data")
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

	t.Log("Initializing real WhatsApp clients...")

	wappClients := make(map[string]WhatsAppClient)

	// Make an in-memory DB for the rest of the app state for blazing fast tests
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("initialize test app database: %v", err)
	}

	// Open the physical DB to harvest WhatsApp session paths AND person assignments
	importDbMgr, err := NewDBManager(appDBPath)
	if err != nil {
		t.Fatalf("open persisted app database: %v", err)
	}
	persons, err := importDbMgr.ListPersons(context.Background())
	if err != nil {
		t.Fatalf("load persisted persons: %v", err)
	}
	for _, p := range persons {
		if _, err := dbMgr.db.Exec("INSERT INTO persons (id, name, slug, created_at) VALUES (?, ?, ?, ?)", p.ID, p.Name, p.Slug, p.CreatedAt); err != nil {
			t.Fatalf("import person %q: %v", p.Name, err)
		}
	}

	sessions, err := importDbMgr.SenderSessions(context.Background())
	if err != nil {
		t.Fatalf("load persisted sender sessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Fatal("no persisted sender sessions available for WhatsApp E2E")
	}
	for _, session := range sessions {
		if _, err := dbMgr.db.Exec("INSERT INTO sender_sessions (phone, db_filename, person_id) VALUES (?, ?, ?)", session.Phone, session.DBFilename, session.PersonID); err != nil {
			t.Fatalf("import sender session %s: %v", session.Phone, err)
		}

		dbForPhone := filepath.Join(dataDir, session.DBFilename)
		client, err := InitWhatsApp(session.Phone, dbForPhone, stateMgr, multiAlerter, "")
		if err != nil {
			t.Fatalf("initialize WhatsApp sender %s: %v", session.Phone, err)
		}
		wappClients[session.Phone] = client
		ensureConn(session.Phone)
	}

	// Hydrate connections with PersonSlug/PersonID/PersonName from DB
	if dbMgr != nil {
		HydrateConnectionPersons(dbMgr, stateMgr, context.Background())
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

	var attemptedCount, sentCount int
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
			attemptedCount++
			if err := SendVoiceNote(client, targetPhone, audioPath); err != nil {
				return err
			}
			sentCount++
			return nil
		},
	}

	currentSong := &SongInfo{}

	t.Log("Triggering song check...")
	(&metadataContestChecker{poller: poller, coordinator: poller.contestCheckCoordinator(), currentSong: currentSong}).Check(activeTime)

	if poller.matchesToday != 1 {
		t.Fatalf("Expected 1 match to trigger message, got %d", poller.matchesToday)
	}
	if attemptedCount != len(wappClients) || sentCount != attemptedCount {
		t.Fatalf("voice note sends: %d successful / %d attempted across %d connected clients", sentCount, attemptedCount, len(wappClients))
	}

	t.Logf("✅ E2E Test finished successfully. Sent %d voice notes across %d connected clients.", sentCount, len(wappClients))
}

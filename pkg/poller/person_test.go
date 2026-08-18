package poller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPersonCRUDAndSlugUniqueness(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	ctx := context.Background()

	p1, err := dbMgr.CreatePerson(ctx, "Victor Stan")
	if err != nil {
		t.Fatalf("CreatePerson(p1) error = %v", err)
	}
	if p1.Slug != "victor-stan" {
		t.Fatalf("p1.Slug = %q, want %q", p1.Slug, "victor-stan")
	}

	// Uniqueness check: same name generates unique slug
	p2, err := dbMgr.CreatePerson(ctx, "Victor Stan")
	if err != nil {
		t.Fatalf("CreatePerson(p2) error = %v", err)
	}
	if p2.Slug != "victor-stan-2" {
		t.Fatalf("p2.Slug = %q, want %q", p2.Slug, "victor-stan-2")
	}

	list, err := dbMgr.ListPersons(ctx)
	if err != nil {
		t.Fatalf("ListPersons() error = %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len(list) = %d, want 2", len(list))
	}

	// Update
	if err := dbMgr.UpdatePerson(ctx, p1.ID, "Victor Alexandru"); err != nil {
		t.Fatalf("UpdatePerson() error = %v", err)
	}
	updated, err := dbMgr.GetPerson(ctx, p1.ID)
	if err != nil {
		t.Fatalf("GetPerson() error = %v", err)
	}
	if updated.Name != "Victor Alexandru" || updated.Slug != "victor-alexandru" {
		t.Fatalf("updated = %+v, want Victor Alexandru", updated)
	}

	// Delete
	if err := dbMgr.DeletePerson(ctx, p2.ID); err != nil {
		t.Fatalf("DeletePerson() error = %v", err)
	}
	listAfterDelete, err := dbMgr.ListPersons(ctx)
	if err != nil {
		t.Fatalf("ListPersons() after delete error = %v", err)
	}
	if len(listAfterDelete) != 1 {
		t.Fatalf("len(listAfterDelete) = %d, want 1", len(listAfterDelete))
	}
}

func TestSenderSessionPersonAssignment(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	ctx := context.Background()

	person, err := dbMgr.CreatePerson(ctx, "Andrei Popescu")
	if err != nil {
		t.Fatalf("CreatePerson() error = %v", err)
	}

	phone1 := "+40711111111"
	phone2 := "+40722222222"

	if err := dbMgr.SetSenderSessionWithPerson(ctx, phone1, "wapp_40711111111.sqlite", &person.ID); err != nil {
		t.Fatalf("SetSenderSessionWithPerson() error = %v", err)
	}
	if err := dbMgr.SetSenderSession(ctx, phone2, "wapp_40722222222.sqlite"); err != nil {
		t.Fatalf("SetSenderSession() error = %v", err)
	}

	sessions, err := dbMgr.SenderSessions(ctx)
	if err != nil {
		t.Fatalf("SenderSessions() error = %v", err)
	}
	if len(sessions) != 2 {
		t.Fatalf("len(sessions) = %d, want 2", len(sessions))
	}

	var s1, s2 *SenderSession
	for i := range sessions {
		switch sessions[i].Phone {
		case phone1:
			s1 = &sessions[i]
		case phone2:
			s2 = &sessions[i]
		}
	}

	if s1 == nil || s1.PersonID == nil || *s1.PersonID != person.ID || s1.PersonName != "Andrei Popescu" {
		t.Fatalf("s1 session = %+v, want assigned to Andrei Popescu", s1)
	}
	if s2 == nil || s2.PersonID != nil {
		t.Fatalf("s2 session = %+v, want unassigned", s2)
	}

	// Assign s2 to person
	if err := dbMgr.AssignPhoneToPerson(ctx, phone2, &person.ID); err != nil {
		t.Fatalf("AssignPhoneToPerson() error = %v", err)
	}
	sessionsAfter, _ := dbMgr.SenderSessions(ctx)
	for _, s := range sessionsAfter {
		if s.Phone == phone2 && (s.PersonID == nil || *s.PersonID != person.ID) {
			t.Fatalf("s2 after assign = %+v, want assigned to person", s)
		}
	}
}

func TestPoller_doTriggerVoiceNote_MultiplePhonesPerPersonDrawDistinctAudios(t *testing.T) {
	audiosDir := t.TempDir()
	personSlug := "victor-stan"
	personDir := filepath.Join(audiosDir, personSlug)
	if err := os.MkdirAll(personDir, 0755); err != nil {
		t.Fatalf("mkdir personDir: %v", err)
	}

	// Create 3 distinct audio files in victor-stan's pool
	_ = os.WriteFile(filepath.Join(personDir, "voice1.ogg"), []byte("audio-content-1"), 0644)
	_ = os.WriteFile(filepath.Join(personDir, "voice2.ogg"), []byte("audio-content-2"), 0644)
	_ = os.WriteFile(filepath.Join(personDir, "voice3.ogg"), []byte("audio-content-3"), 0644)

	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	personID := int64(1)
	stateMgr := NewStateManager()
	stateMgr.Update(func(s *AppState) {
		s.Persons = []Person{{ID: personID, Name: "Victor Stan", Slug: personSlug}}
		s.Connections = []WAConnectionState{
			{Phone: "+40711111111", Status: StatusConnected, WhatsAppConnected: true, PersonID: &personID, PersonSlug: personSlug, PersonName: "Victor Stan"},
			{Phone: "+40722222222", Status: StatusConnected, WhatsAppConnected: true, PersonID: &personID, PersonSlug: personSlug, PersonName: "Victor Stan"},
		}
	})

	var sentAudios []string
	poller := &Poller{
		TargetPhone: "+40770661491",
		StateMgr:    stateMgr,
		Alerter:     NewMultiAlerter(),
		AudiosDir:   audiosDir,
		DBMgr:       dbMgr,
		SendVoiceNote: func(senderPhone, targetPhone, audioPath string) error {
			if _, err := os.Stat(audioPath); err != nil {
				return err
			}
			sentAudios = append(sentAudios, audioPath)
			return nil
		},
	}

	now := bucharestTime(2026, time.June, 17, 12, 0, 0)
	poller.doTriggerVoiceNote(triggerSourceMetadata, "BTS", now, 1, 0)

	if len(sentAudios) != 2 {
		t.Fatalf("sentAudios count = %d, want 2", len(sentAudios))
	}
	if sentAudios[0] == sentAudios[1] {
		t.Fatalf("Both phones sent identical audio file %q! Each phone must draw a distinct file.", sentAudios[0])
	}

	// Verify both were moved to used/ in personDir
	usedEntries, err := os.ReadDir(filepath.Join(personDir, "used"))
	if err != nil {
		t.Fatalf("ReadDir used: %v", err)
	}
	if len(usedEntries) != 2 {
		t.Fatalf("used audio files count = %d, want 2", len(usedEntries))
	}
}

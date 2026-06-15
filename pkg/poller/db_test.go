package poller

import (
	"context"
	"testing"
	"time"
)

func TestSongDeduplication(t *testing.T) {
	// Initialize an in-memory SQLite database
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to initialize in-memory DB: %v", err)
	}

	ctx := context.Background()
	artist := "KAMRAD"
	title := "BE MINE (NEW PW)"
	now := time.Now()

	// 1. Should not have played today initially
	played, err := dbMgr.HasSongPlayedToday(ctx, artist, title, now)
	if err != nil {
		t.Fatalf("HasSongPlayedToday returned error: %v", err)
	}
	if played {
		t.Fatalf("Expected song to not be played yet, but it was")
	}

	// 2. Record it as played
	err = dbMgr.RecordSongPlay(ctx, artist, title, now)
	if err != nil {
		t.Fatalf("RecordSongPlay returned error: %v", err)
	}

	// 3. Verify it is now marked as played today
	played, err = dbMgr.HasSongPlayedToday(ctx, artist, title, now)
	if err != nil {
		t.Fatalf("HasSongPlayedToday returned error: %v", err)
	}
	if !played {
		t.Fatalf("Expected song to be marked as played, but it wasn't")
	}

	// 4. Verify it is NOT marked as played tomorrow
	tomorrow := now.Add(24 * time.Hour)
	played, err = dbMgr.HasSongPlayedToday(ctx, artist, title, tomorrow)
	if err != nil {
		t.Fatalf("HasSongPlayedToday returned error for tomorrow: %v", err)
	}
	if played {
		t.Fatalf("Expected song to NOT be marked as played tomorrow, but it was")
	}

	// 5. Test duplicates don't throw an error due to UNIQUE constraint (INSERT OR IGNORE)
	err = dbMgr.RecordSongPlay(ctx, artist, title, now)
	if err != nil {
		t.Fatalf("RecordSongPlay returned error on duplicate insert: %v", err)
	}
}

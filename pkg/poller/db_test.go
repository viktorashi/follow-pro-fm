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
	now := time.Date(2026, time.June, 15, 12, 0, 0, 0, time.UTC)

	// 1. Should not have played in the last half hour initially
	played, err := dbMgr.HasSongPlayedTheLastHalfHour(ctx, artist, title, now)
	if err != nil {
		t.Fatalf("HasSongPlayedTheLastHalfHour returned error: %v", err)
	}
	if played {
		t.Fatalf("Expected song to not be played yet, but it was")
	}

	// 2. Record it as played
	err = dbMgr.RecordSongPlay(ctx, artist, title, now)
	if err != nil {
		t.Fatalf("RecordSongPlay returned error: %v", err)
	}

	// 3. Verify it is now marked as played at the same time
	played, err = dbMgr.HasSongPlayedTheLastHalfHour(ctx, artist, title, now)
	if err != nil {
		t.Fatalf("HasSongPlayedTheLastHalfHour returned error: %v", err)
	}
	if !played {
		t.Fatalf("Expected song to be marked as played, but it wasn't")
	}

	// 4. Verify it is marked as played 15 minutes later
	fifteenMinutesLater := now.Add(15 * time.Minute)
	played, err = dbMgr.HasSongPlayedTheLastHalfHour(ctx, artist, title, fifteenMinutesLater)
	if err != nil {
		t.Fatalf("HasSongPlayedTheLastHalfHour returned error for 15 mins later: %v", err)
	}
	if !played {
		t.Fatalf("Expected song to be marked as played 15 mins later, but it wasn't")
	}

	// 5. Verify it is NOT marked as played 31 minutes later
	thirtyOneMinutesLater := now.Add(31 * time.Minute)
	played, err = dbMgr.HasSongPlayedTheLastHalfHour(ctx, artist, title, thirtyOneMinutesLater)
	if err != nil {
		t.Fatalf("HasSongPlayedTheLastHalfHour returned error for 31 mins later: %v", err)
	}
	if played {
		t.Fatalf("Expected song to NOT be marked as played 31 mins later, but it was")
	}

	// 6. Test duplicates don't throw an error due to UNIQUE constraint (INSERT OR IGNORE)
	err = dbMgr.RecordSongPlay(ctx, artist, title, now)
	if err != nil {
		t.Fatalf("RecordSongPlay returned error on duplicate insert: %v", err)
	}
}

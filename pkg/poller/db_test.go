package poller

import (
	"context"
	"testing"
	"time"
)

func TestWasSongInLastNPlays(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to initialize in-memory DB: %v", err)
	}

	ctx := context.Background()
	now := time.Now()

	// Initially, song should not be in the last 2 plays
	played, err := dbMgr.WasSongInLastNPlays(ctx, "BTS", "Dynamite", 2)
	if err != nil {
		t.Fatalf("WasSongInLastNPlays returned error: %v", err)
	}
	if played {
		t.Fatalf("Expected song to not be in last 2 plays")
	}

	// 1. Log song 1
	_ = dbMgr.LogRadioSong(ctx, "BTS", "Dynamite", now)

	// Now it should be in the last 2 plays
	played, _ = dbMgr.WasSongInLastNPlays(ctx, "BTS", "Dynamite", 2)
	if !played {
		t.Fatalf("Expected BTS - Dynamite to be in last 2 plays")
	}

	// 2. Log song 2
	_ = dbMgr.LogRadioSong(ctx, "Ed Sheeran", "Shape of You", now)

	// Still in last 2 plays (BTS was 1 play ago)
	played, _ = dbMgr.WasSongInLastNPlays(ctx, "BTS", "Dynamite", 2)
	if !played {
		t.Fatalf("Expected BTS - Dynamite to be in last 2 plays")
	}

	// 3. Log song 3
	_ = dbMgr.LogRadioSong(ctx, "The Weeknd", "Blinding Lights", now)

	// Now BTS is 3 plays ago, so it should NOT be in the last 2 plays
	played, _ = dbMgr.WasSongInLastNPlays(ctx, "BTS", "Dynamite", 2)
	if played {
		t.Fatalf("Expected BTS - Dynamite to NOT be in last 2 plays")
	}
}

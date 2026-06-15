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

	// 1. Log BTS (first play)
	_ = dbMgr.LogRadioSong(ctx, "BTS", "Dynamite", now)

	// Since OFFSET is 1, checking for BTS immediately (as if we just logged it) should NOT find it in the PREVIOUS 2 plays
	played, _ := dbMgr.WasSongInLastNPlays(ctx, "BTS", "Dynamite", 2)
	if played {
		t.Fatalf("Expected song to NOT be in previous 2 plays (it is the current play)")
	}

	// 2. Log Kamrad
	_ = dbMgr.LogRadioSong(ctx, "Kamrad", "BE MINE", now)

	// Now we simulate BTS playing again. First we log it.
	_ = dbMgr.LogRadioSong(ctx, "BTS", "Dynamite", now)

	// Now we check if BTS was in the PREVIOUS 2 plays (before this current BTS).
	// The table has: BTS (1), Kamrad (2), BTS (3).
	// OFFSET 1 skips 3. The previous 2 are Kamrad (2) and BTS (1).
	// So it SHOULD find it!
	played, _ = dbMgr.WasSongInLastNPlays(ctx, "BTS", "Dynamite", 2)
	if !played {
		t.Fatalf("Expected BTS - Dynamite to be in previous 2 plays")
	}

	// 3. Let's add 2 other songs to push the first BTS out of the top 2 previous
	_ = dbMgr.LogRadioSong(ctx, "Ed Sheeran", "Shape of You", now)
	_ = dbMgr.LogRadioSong(ctx, "The Weeknd", "Blinding Lights", now)

	// Simulate BTS playing again
	_ = dbMgr.LogRadioSong(ctx, "BTS", "Dynamite", now)

	// Table: BTS(1), Kamrad(2), BTS(3), Ed Sheeran(4), The Weeknd(5), BTS(6)
	// OFFSET 1 skips BTS(6). The previous 2 are The Weeknd(5) and Ed Sheeran(4).
	// Neither is BTS! So it should NOT be found!
	played, _ = dbMgr.WasSongInLastNPlays(ctx, "BTS", "Dynamite", 2)
	if played {
		t.Fatalf("Expected BTS - Dynamite to NOT be in previous 2 plays")
	}
}

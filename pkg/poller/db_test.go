package poller

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestTrustedEmailsFilePath(t *testing.T) {
	dbPath := filepath.Join("/tmp", "profm", "app.sqlite")
	want := filepath.Join("/tmp", "profm", "trusted-emails.txt")
	if got := TrustedEmailsFilePath(dbPath); got != want {
		t.Fatalf("TrustedEmailsFilePath() = %q, want %q", got, want)
	}
}

func TestGatheringSignaturesSettingDefaultsToTrueAndPersists(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}

	active, err := dbMgr.IsGatheringSignaturesEnabled(context.Background())
	if err != nil {
		t.Fatalf("IsGatheringSignaturesEnabled() error = %v", err)
	}
	if !active {
		t.Fatal("expected gathering signatures to default to enabled")
	}

	if err := dbMgr.SetGatheringSignatures(context.Background(), false); err != nil {
		t.Fatalf("SetGatheringSignatures(false) error = %v", err)
	}

	active, err = dbMgr.IsGatheringSignaturesEnabled(context.Background())
	if err != nil {
		t.Fatalf("IsGatheringSignaturesEnabled() error = %v", err)
	}
	if active {
		t.Fatal("expected gathering signatures to persist as disabled")
	}
}

func TestWasSongInLastNPlays(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to initialize in-memory DB: %v", err)
	}

	ctx := context.Background()
	now := time.Now()

	// 1. Log BTS (first play)
	_, _ = dbMgr.LogRadioSong(ctx, "BTS", "Dynamite", now)

	// Since OFFSET is 1, checking for BTS immediately (as if we just logged it) should NOT find it in the PREVIOUS 2 plays
	played, _ := dbMgr.WasSongInLastNPlays(ctx, "BTS", "Dynamite", 2)
	if played {
		t.Fatalf("Expected song to NOT be in previous 2 plays (it is the current play)")
	}

	// 2. Log Kamrad
	_, _ = dbMgr.LogRadioSong(ctx, "Kamrad", "BE MINE", now)

	// Now we simulate BTS playing again. First we log it.
	_, _ = dbMgr.LogRadioSong(ctx, "BTS", "Dynamite", now)

	// Now we check if BTS was in the PREVIOUS 2 plays (before this current BTS).
	// The table has: BTS (1), Kamrad (2), BTS (3).
	// OFFSET 1 skips 3. The previous 2 are Kamrad (2) and BTS (1).
	// So it SHOULD find it!
	played, _ = dbMgr.WasSongInLastNPlays(ctx, "BTS", "Dynamite", 2)
	if !played {
		t.Fatalf("Expected BTS - Dynamite to be in previous 2 plays")
	}

	// 3. Let's add 2 other songs to push the first BTS out of the top 2 previous
	_, _ = dbMgr.LogRadioSong(ctx, "Ed Sheeran", "Shape of You", now)
	_, _ = dbMgr.LogRadioSong(ctx, "The Weeknd", "Blinding Lights", now)

	// Simulate BTS playing again
	_, _ = dbMgr.LogRadioSong(ctx, "BTS", "Dynamite", now)

	// Table: BTS(1), Kamrad(2), BTS(3), Ed Sheeran(4), The Weeknd(5), BTS(6)
	// OFFSET 1 skips BTS(6). The previous 2 are The Weeknd(5) and Ed Sheeran(4).
	// Neither is BTS! So it should NOT be found!
	played, _ = dbMgr.WasSongInLastNPlays(ctx, "BTS", "Dynamite", 2)
	if played {
		t.Fatalf("Expected BTS - Dynamite to NOT be in previous 2 plays")
	}
}

func TestCanSendCampaignArtistRequiresDifferentArtistBetweenSends(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to initialize in-memory DB: %v", err)
	}

	ctx := context.Background()
	now := time.Now()

	firstBTSLogID, err := dbMgr.LogRadioSong(ctx, "BTS", "Dynamite", now)
	if err != nil {
		t.Fatalf("LogRadioSong() error = %v", err)
	}
	if err := dbMgr.RecordSuccessfulSend(ctx, "BTS", "BTS", "Dynamite", "hash-1", firstBTSLogID, now); err != nil {
		t.Fatalf("RecordSuccessfulSend() error = %v", err)
	}

	secondBTSLogID, err := dbMgr.LogRadioSong(ctx, "BTS feat. Halsey", "Butter", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("LogRadioSong() error = %v", err)
	}
	allowed, err := dbMgr.CanSendCampaignArtist(ctx, "BTS", secondBTSLogID)
	if err != nil {
		t.Fatalf("CanSendCampaignArtist() error = %v", err)
	}
	if allowed {
		t.Fatalf("Expected resend to be blocked without a different artist in between")
	}

	if _, err := dbMgr.LogRadioSong(ctx, "Kamrad", "BE MINE", now.Add(2*time.Minute)); err != nil {
		t.Fatalf("LogRadioSong() error = %v", err)
	}
	thirdBTSLogID, err := dbMgr.LogRadioSong(ctx, "BTS", "Permission to Dance", now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("LogRadioSong() error = %v", err)
	}
	allowed, err = dbMgr.CanSendCampaignArtist(ctx, "BTS", thirdBTSLogID)
	if err != nil {
		t.Fatalf("CanSendCampaignArtist() error = %v", err)
	}
	if !allowed {
		t.Fatalf("Expected resend to be allowed after a different artist was logged")
	}
}

func TestCanSendCampaignArtistBlocksFingerprintResendUntilAnotherArtistIsLogged(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("Failed to initialize in-memory DB: %v", err)
	}

	ctx := context.Background()
	now := time.Now()

	firstBTSLogID, err := dbMgr.LogRadioSong(ctx, "BTS", "Dynamite", now)
	if err != nil {
		t.Fatalf("LogRadioSong() error = %v", err)
	}
	if err := dbMgr.RecordSuccessfulSend(ctx, "BTS", "BTS", "Dynamite", "hash-1", firstBTSLogID, now); err != nil {
		t.Fatalf("RecordSuccessfulSend() error = %v", err)
	}

	allowed, err := dbMgr.CanSendCampaignArtist(ctx, "BTS", firstBTSLogID+1)
	if err != nil {
		t.Fatalf("CanSendCampaignArtist() error = %v", err)
	}
	if allowed {
		t.Fatal("Expected fingerprint resend boundary to stay blocked before another artist is logged")
	}

	if _, err := dbMgr.LogRadioSong(ctx, "Kamrad", "BE MINE", now.Add(time.Minute)); err != nil {
		t.Fatalf("LogRadioSong() error = %v", err)
	}

	allowed, err = dbMgr.CanSendCampaignArtist(ctx, "BTS", firstBTSLogID+2)
	if err != nil {
		t.Fatalf("CanSendCampaignArtist() error = %v", err)
	}
	if !allowed {
		t.Fatal("Expected fingerprint resend boundary to open once a non-campaign artist is logged")
	}
}

func TestSenderSessionsPersistDiscoveredPhoneAndDatabase(t *testing.T) {
	dbMgr, err := NewDBManager(":memory:")
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}
	ctx := context.Background()
	if err := dbMgr.SetSenderSession(ctx, "+40111222333", "wapp_pairing_abc.sqlite"); err != nil {
		t.Fatalf("SetSenderSession() error = %v", err)
	}

	sessions, err := dbMgr.SenderSessions(ctx)
	if err != nil {
		t.Fatalf("SenderSessions() error = %v", err)
	}
	if len(sessions) != 1 || sessions[0].Phone != "+40111222333" || sessions[0].DBFilename != "wapp_pairing_abc.sqlite" {
		t.Fatalf("SenderSessions() = %+v", sessions)
	}
}

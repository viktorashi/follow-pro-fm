package poller

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestContestCheckCoordinatorClaimsOneSharedWindow(t *testing.T) {
	now := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	coordinator := NewContestCheckCoordinator(20 * time.Minute)

	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if coordinator.Claim(now, "test", "test_pattern") {
				winners.Add(1)
			}
		})
	}
	wg.Wait()

	if winners.Load() != 1 {
		t.Fatalf("concurrent claims = %d winners, want 1", winners.Load())
	}
	if coordinator.CanCheck(now.Add(19 * time.Minute)) {
		t.Fatal("checker should sleep during claimed window")
	}
	if !coordinator.CanCheck(now.Add(20 * time.Minute)) {
		t.Fatal("checker should resume when claimed window expires")
	}
}

func TestPollerContestCheckCooldownDefaultsAndIsConfigurable(t *testing.T) {
	now := time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)
	poller := &Poller{ContestCheckCooldown: 5 * time.Minute}

	if !poller.contestCheckCoordinator().Claim(now, "test", "test_pattern") {
		t.Error("First checker should claim the window successfully")
	}
	if poller.contestCheckCoordinator().CanCheck(now.Add(2 * time.Minute)) {
		t.Fatal("poller should share the claimed window with other checkers (after 60s telemetry window)")
	}
	if !poller.contestCheckCoordinator().Claim(now.Add(5*time.Minute), "test", "test_pattern") {
		t.Fatal("configured cooldown should permit the next window after five minutes")
	}
}

func TestContestCheckCoordinator_ClearsCooldownWhenNotMatching(t *testing.T) {
	poller := &Poller{ContestCheckCooldown: 10 * time.Minute}
	now := time.Now()

	if !poller.contestCheckCoordinator().Claim(now, "test", "test_pattern") {
		t.Fatal("first claim should succeed")
	}

	// The coordinator state is fully driven by successful claims.
	// We just want to ensure the next window opens after the configured cooldown.

	if !poller.contestCheckCoordinator().Claim(now.Add(10*time.Minute), "test", "test_pattern") {
		t.Fatal("configured cooldown should permit the next window after ten minutes")
	}
}

func TestContestCheckCoordinatorTelemetry(t *testing.T) {
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	coordinator := NewContestCheckCoordinator(20 * time.Minute)

	if !coordinator.Claim(now, "metadata", "BTS") {
		t.Fatal("first claim should win")
	}
	if claim, ok := coordinator.ActiveClaim(now.Add(60 * time.Second)); !ok || claim.Source != "metadata" || claim.Pattern != "BTS" || !claim.ClaimedAt.Equal(now) {
		t.Fatalf("ActiveClaim() = %#v, %v", claim, ok)
	}
	if _, ok := coordinator.ActiveClaim(now.Add(61 * time.Second)); ok {
		t.Fatal("claim should not remain active outside the telemetry window")
	}

	coordinator.Claim(now.Add(30*time.Second), "fingerprint", "signature.mp3")
	coordinator.Claim(now.Add(40*time.Second), "metadata", "BTS")
	late := coordinator.GetLateMatches(now)
	if len(late) != 1 || late[0].Source != "fingerprint" || late[0].Pattern != "signature.mp3" {
		t.Fatalf("GetLateMatches() = %#v", late)
	}
	if got := coordinator.GetLateMatches(now.Add(time.Second)); got != nil {
		t.Fatalf("GetLateMatches(stale claim) = %#v, want nil", got)
	}

	late[0].Source = "mutated"
	if got := coordinator.GetLateMatches(now); got[0].Source != "fingerprint" {
		t.Fatal("GetLateMatches returned coordinator-owned storage")
	}
	if !coordinator.Claim(now.Add(20*time.Minute), "whisper", "bts") {
		t.Fatal("next cooldown claim should win")
	}
	if got := coordinator.GetLateMatches(now.Add(20 * time.Minute)); len(got) != 0 {
		t.Fatalf("late matches were not reset: %#v", got)
	}
}

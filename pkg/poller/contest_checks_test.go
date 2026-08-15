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
		wg.Add(1)
		go func() {
			defer wg.Done()
			if coordinator.Claim(now, "test") {
				winners.Add(1)
			}
		}()
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

	if !poller.contestCheckCoordinator().Claim(now, "test") {
		t.Error("First checker should claim the window successfully")
	}
	if poller.contestCheckCoordinator().CanCheck(now.Add(2 * time.Minute)) {
		t.Fatal("poller should share the claimed window with other checkers (after 60s telemetry window)")
	}
	if !poller.contestCheckCoordinator().Claim(now.Add(5*time.Minute), "test") {
		t.Fatal("configured cooldown should permit the next window after five minutes")
	}
}

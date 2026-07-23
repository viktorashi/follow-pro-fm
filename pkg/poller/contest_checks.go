package poller

import (
	"log"
	"sync"
	"time"
)

const DefaultContestCheckCooldown = 20 * time.Minute

// Compile-time assertion: if anyone ever makes DefaultContestCheckCooldown
// negative this line will refuse to compile (uint cannot hold a negative value).
const _ = uint(DefaultContestCheckCooldown)

// ContestChecker is a detection source that may try to claim the shared
// contest window after it finds a campaign candidate.
type ContestChecker interface {
	Check(time.Time)
}

// ContestCheckCoordinator gives every detection source one shared cooldown.
// A checker claims it only after finding a campaign candidate.
type ContestCheckCoordinator struct {
	mu            sync.Mutex
	cooldown      time.Duration
	until         time.Time
	lastClaimedAt time.Time
	lastClaimedBy string
}

func NewContestCheckCoordinator(cooldown time.Duration) *ContestCheckCoordinator {
	if cooldown <= 0 {
		cooldown = DefaultContestCheckCooldown
	}
	return &ContestCheckCoordinator{cooldown: cooldown}
}

func (c *ContestCheckCoordinator) CanCheck(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Allow checking if not in cooldown OR if we are within 60s of the last claim
	// This ensures other checkers can still trigger shortly after the first one for telemetry
	return !now.Before(c.until) || now.Sub(c.lastClaimedAt) <= 60*time.Second
}

func (c *ContestCheckCoordinator) Claim(now time.Time, source string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if now.Before(c.until) {
		// It's already claimed, but log telemetry if it was claimed recently
		diff := now.Sub(c.lastClaimedAt)
		if diff <= 60*time.Second && c.lastClaimedBy != source {
			log.Printf("   📊 [TELEMETRY] Checker %q triggered %v after the first checker (%q)", source, diff.Round(10*time.Millisecond), c.lastClaimedBy)
		}
		return false
	}

	log.Printf("   📊 [TELEMETRY] Checker %q was the FIRST to trigger!", source)
	c.until = now.Add(c.cooldown)
	c.lastClaimedAt = now
	c.lastClaimedBy = source
	return true
}

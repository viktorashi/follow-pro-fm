package poller

import (
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
	mu       sync.Mutex
	cooldown time.Duration
	until    time.Time
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
	return !now.Before(c.until)
}

func (c *ContestCheckCoordinator) Claim(now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Before(c.until) {
		return false
	}
	c.until = now.Add(c.cooldown)
	return true
}

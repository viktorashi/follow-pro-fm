package poller

import (
	"sync"
	"time"
)

const DefaultContestCheckCooldown = 20 * time.Minute

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

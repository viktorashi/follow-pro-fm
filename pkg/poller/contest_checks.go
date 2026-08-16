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
	mu               sync.Mutex
	cooldown         time.Duration
	until            time.Time
	lastClaimedAt    time.Time
	lastClaimedBy    string
	lastClaimPattern string
	lateMatches      []ClaimDetails
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

func (c *ContestCheckCoordinator) Claim(now time.Time, source string, pattern string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if now.Before(c.until) {
		diff := now.Sub(c.lastClaimedAt)
		if diff <= 60*time.Second && c.lastClaimedBy != source {
			c.lateMatches = append(c.lateMatches, ClaimDetails{Source: source, Pattern: pattern, ClaimedAt: now})
			log.Printf("   📊 [TELEMETRY] Checker %q (%q) triggered %v after the first checker (%q: %q)", source, pattern, diff.Round(10*time.Millisecond), c.lastClaimedBy, c.lastClaimPattern)
		}
		return false
	}

	log.Printf("   📊 [TELEMETRY] Checker %q (%q) was the FIRST to trigger!", source, pattern)
	c.until = now.Add(c.cooldown)
	c.lastClaimedAt = now
	c.lastClaimedBy = source
	c.lastClaimPattern = pattern
	c.lateMatches = nil // reset for new claim
	return true
}

// ClaimDetails holds the winner's identity for loser diagnostics.
type ClaimDetails struct {
	Source    string
	Pattern   string
	ClaimedAt time.Time
}

// ActiveClaim returns the current winner's info if we're still inside the
// 60-second telemetry window after a claim. Returns false if no recent claim
// exists (losers outside the window should not dump).
func (c *ContestCheckCoordinator) ActiveClaim(now time.Time) (ClaimDetails, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Before(c.until) && now.Sub(c.lastClaimedAt) <= 60*time.Second {
		return ClaimDetails{
			Source:    c.lastClaimedBy,
			Pattern:   c.lastClaimPattern,
			ClaimedAt: c.lastClaimedAt,
		}, true
	}
	return ClaimDetails{}, false
}

// GetLateMatches returns all late matches that occurred during the 60s telemetry window of a specific claim.
func (c *ContestCheckCoordinator) GetLateMatches(claimedAt time.Time) []ClaimDetails {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastClaimedAt.Equal(claimedAt) {
		return append([]ClaimDetails(nil), c.lateMatches...)
	}
	return nil
}

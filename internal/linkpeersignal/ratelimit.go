package linkpeersignal

import (
	"sync"
	"time"
)

// RateLimiter is a per-key token bucket. Keys may be devId, IP, etc. Each key
// refills at `perHour` tokens/hour, capped at `perHour` (no burst beyond the
// hourly budget). A background Sweep drops idle buckets so a flood of unique
// keys can't grow memory unbounded.
//
// This implements the devId-primary / IP-secondary rate limiting of
// SIGNAL_SPEC §6 (anti-bruteforce without false-positiving shared-NAT users).
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	perHour int
	maxIdle time.Duration
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewRateLimiter(perHour int) *RateLimiter {
	if perHour <= 0 {
		perHour = 1
	}
	return &RateLimiter{
		buckets: map[string]*bucket{},
		perHour: perHour,
		maxIdle: time.Hour,
		now:     time.Now,
	}
}

// Allow consumes one token; returns false if the key is rate-limited.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := rl.now()
	b, ok := rl.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(rl.perHour), last: now}
		rl.buckets[key] = b
	}
	// refill proportional to elapsed time, capped at perHour
	elapsedH := now.Sub(b.last).Hours()
	b.tokens += elapsedH * float64(rl.perHour)
	if b.tokens > float64(rl.perHour) {
		b.tokens = float64(rl.perHour)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Sweep drops buckets idle longer than maxIdle. Caller runs it periodically
// (main wires a 30s ticker).
func (rl *RateLimiter) Sweep() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := rl.now().Add(-rl.maxIdle)
	for k, b := range rl.buckets {
		if b.last.Before(cutoff) {
			delete(rl.buckets, k)
		}
	}
}

// SetNow overrides the clock (tests).
func (rl *RateLimiter) SetNow(now func() time.Time) { rl.now = now }

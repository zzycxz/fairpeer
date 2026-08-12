package linkpeersignal

import (
	"testing"
	"time"
)

func TestRateLimiterAllowUntilExhausted(t *testing.T) {
	now := time.Unix(1700000000, 0)
	rl := NewRateLimiter(3)
	rl.SetNow(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		if !rl.Allow("k") {
			t.Fatalf("attempt %d should allow", i)
		}
	}
	if rl.Allow("k") {
		t.Fatal("4th should be denied")
	}
}

func TestRateLimiterRefill(t *testing.T) {
	now := time.Unix(1700000000, 0)
	rl := NewRateLimiter(3)
	rl.SetNow(func() time.Time { return now })
	for i := 0; i < 3; i++ {
		rl.Allow("k")
	}
	// 1 hour later → fully refilled (3/hour budget)
	rl.SetNow(func() time.Time { return now.Add(time.Hour) })
	if !rl.Allow("k") {
		t.Fatal("should allow after refill")
	}
}

func TestRateLimiterIndependentKeys(t *testing.T) {
	rl := NewRateLimiter(1)
	if !rl.Allow("a") {
		t.Fatal("a should allow")
	}
	if !rl.Allow("b") {
		t.Fatal("b should allow (separate bucket)")
	}
	if rl.Allow("a") {
		t.Fatal("a second should deny")
	}
}

func TestRateLimiterSweep(t *testing.T) {
	now := time.Unix(1700000000, 0)
	rl := NewRateLimiter(1)
	rl.SetNow(func() time.Time { return now })
	rl.Allow("old")
	rl.SetNow(func() time.Time { return now.Add(2 * time.Hour) })
	rl.Sweep()
	// "old" bucket should be gone → fresh bucket allows immediately
	if !rl.Allow("old") {
		t.Fatal("after sweep, key should be fresh")
	}
}

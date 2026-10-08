package middleware

import (
	"testing"
	"time"
)

func TestRateLimiter_AllowsUpToLimit(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)

	for i := 0; i < 3; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("4th request should be denied")
	}
}

func TestRateLimiter_IsolatesByIP(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)

	if !rl.Allow("1.2.3.4") {
		t.Fatal("first request from ip A should be allowed")
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("second request from ip A should be denied")
	}
	if !rl.Allow("5.6.7.8") {
		t.Fatal("first request from ip B should be allowed")
	}
}

func TestRateLimiter_ResetsAfterWindow(t *testing.T) {
	rl := NewRateLimiter(1, 20*time.Millisecond)

	if !rl.Allow("1.2.3.4") {
		t.Fatal("first request should be allowed")
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("second request should be denied before window expires")
	}

	time.Sleep(40 * time.Millisecond)

	if !rl.Allow("1.2.3.4") {
		t.Fatal("request after window expiry should be allowed again")
	}
}

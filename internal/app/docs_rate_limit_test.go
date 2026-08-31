package app

import (
	"testing"
	"time"
)

func TestDocsRateLimiterUsesIndependentPerUserTokenBuckets(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := newDocsRateLimiter(2, 16, time.Hour, func() time.Time { return now })

	for _, uid := range []int64{1, 1, 2, 2} {
		allowed, retryAfter := limiter.Allow(uid)
		if !allowed || retryAfter != 0 {
			t.Fatalf("uid %d allowed/retry = %t/%v", uid, allowed, retryAfter)
		}
	}
	if allowed, retryAfter := limiter.Allow(1); allowed || retryAfter != 30*time.Second {
		t.Fatalf("third request allowed/retry = %t/%v", allowed, retryAfter)
	}
	now = now.Add(30 * time.Second)
	if allowed, retryAfter := limiter.Allow(1); !allowed || retryAfter != 0 {
		t.Fatalf("refilled request allowed/retry = %t/%v", allowed, retryAfter)
	}
}

func TestDocsRateLimiterEvictsIdleUsersButNeverExceedsItsBound(t *testing.T) {
	now := time.Unix(100, 0)
	limiter := newDocsRateLimiter(1, 2, time.Minute, func() time.Time { return now })
	if allowed, _ := limiter.Allow(1); !allowed {
		t.Fatal("uid 1 was unexpectedly limited")
	}
	if allowed, _ := limiter.Allow(2); !allowed {
		t.Fatal("uid 2 was unexpectedly limited")
	}
	now = now.Add(2 * time.Minute)
	if allowed, _ := limiter.Allow(3); !allowed {
		t.Fatal("idle eviction did not admit uid 3")
	}
	if got := limiter.userCount(); got > 2 {
		t.Fatalf("user count = %d, want <= 2", got)
	}
}

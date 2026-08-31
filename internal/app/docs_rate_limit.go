package app

import (
	"math"
	"sync"
	"time"
)

const (
	docsRateLimiterMaxUsers = 4096
	docsRateLimiterIdleTTL  = 30 * time.Minute
)

type docsRateBucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
}

type docsRateLimiter struct {
	mu       sync.Mutex
	rate     float64
	capacity float64
	maxUsers int
	idleTTL  time.Duration
	now      func() time.Time
	users    map[int64]*docsRateBucket
}

func newDocsRateLimiter(requestsPerMinute, maxUsers int, idleTTL time.Duration, now func() time.Time) *docsRateLimiter {
	if requestsPerMinute < 1 || maxUsers < 1 || idleTTL <= 0 || now == nil {
		return nil
	}
	return &docsRateLimiter{
		rate:     float64(requestsPerMinute) / 60,
		capacity: float64(requestsPerMinute),
		maxUsers: maxUsers,
		idleTTL:  idleTTL,
		now:      now,
		users:    make(map[int64]*docsRateBucket),
	}
}

func (l *docsRateLimiter) Allow(userID int64) (bool, time.Duration) {
	if l == nil || userID <= 0 {
		return false, time.Minute
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	bucket := l.users[userID]
	if bucket == nil {
		l.evictIdle(now)
		if len(l.users) >= l.maxUsers {
			return false, l.idleTTL
		}
		bucket = &docsRateBucket{tokens: l.capacity, updated: now, lastSeen: now}
		l.users[userID] = bucket
	}

	elapsed := now.Sub(bucket.updated).Seconds()
	if elapsed > 0 {
		bucket.tokens = math.Min(l.capacity, bucket.tokens+elapsed*l.rate)
		bucket.updated = now
	}
	bucket.lastSeen = now
	if bucket.tokens >= 1 {
		bucket.tokens--
		return true, 0
	}
	seconds := (1 - bucket.tokens) / l.rate
	return false, time.Duration(math.Ceil(seconds * float64(time.Second)))
}

func (l *docsRateLimiter) evictIdle(now time.Time) {
	for userID, bucket := range l.users {
		if now.Sub(bucket.lastSeen) >= l.idleTTL {
			delete(l.users, userID)
		}
	}
}

func (l *docsRateLimiter) userCount() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.users)
}

package setup

import (
	"sync"
	"time"
)

type rateLimitBucket struct {
	startedAt time.Time
	count     uint64
}

type rateLimitDecision struct {
	allowed   bool
	limit     uint64
	remaining uint64
	resetAt   time.Time
}

// memoryRateLimiter is deliberately process-local because setup is specified
// as a single-instance mode and cannot depend on a database before setup.
type memoryRateLimiter struct {
	mu         sync.Mutex
	limit      uint64
	window     time.Duration
	maxEntries int
	now        func() time.Time
	buckets    map[string]rateLimitBucket
}

func newMemoryRateLimiter(
	config RateLimitConfig,
	now func() time.Time,
) *memoryRateLimiter {
	return &memoryRateLimiter{
		limit:      config.Limit,
		window:     config.Window,
		maxEntries: config.MaxEntries,
		now:        now,
		buckets:    make(map[string]rateLimitBucket),
	}
}

func (l *memoryRateLimiter) consume(key string) rateLimitDecision {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, exists := l.buckets[key]
	if exists && now.Sub(bucket.startedAt) >= l.window {
		delete(l.buckets, key)
		exists = false
	}
	if !exists && len(l.buckets) >= l.maxEntries {
		l.pruneExpired(now)
	}
	if !exists && len(l.buckets) >= l.maxEntries {
		return rateLimitDecision{
			allowed: false,
			limit:   l.limit,
			resetAt: now.Add(l.window),
		}
	}
	if !exists {
		bucket = rateLimitBucket{startedAt: now}
	}

	bucket.count++
	l.buckets[key] = bucket
	remaining := uint64(0)
	if bucket.count < l.limit {
		remaining = l.limit - bucket.count
	}
	return rateLimitDecision{
		allowed:   bucket.count <= l.limit,
		limit:     l.limit,
		remaining: remaining,
		resetAt:   bucket.startedAt.Add(l.window),
	}
}

func (l *memoryRateLimiter) pruneExpired(now time.Time) {
	for key, bucket := range l.buckets {
		if now.Sub(bucket.startedAt) >= l.window {
			delete(l.buckets, key)
		}
	}
}

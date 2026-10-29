package controller

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// chainRateLimiter is a small in-process token bucket keyed by route+client.
// The panel runs one process per panel; a per-replica limit is the intended
// semantics (multi-replica deployments get one bucket set per replica).
type chainRateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*tokenBucket
	lastSweep time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

func newChainRateLimiter() *chainRateLimiter {
	return &chainRateLimiter{buckets: map[string]*tokenBucket{}, lastSweep: time.Now()}
}

func (l *chainRateLimiter) allow(key string, perMinute, burst int) bool {
	if perMinute <= 0 {
		return true
	}
	rate := float64(perMinute) / 60
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if len(l.buckets) > 4096 && now.Sub(l.lastSweep) > 10*time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: float64(burst), last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rate
	if max := float64(burst); b.tokens > max {
		b.tokens = max
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

var chainLimits = newChainRateLimiter()

// chainLimit guards the expensive chain endpoints (import/probe/apply/export).
func chainLimit(scope string, perMinute, burst int) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := scope + "|" + c.ClientIP()
		if !chainLimits.allow(key, perMinute, burst) {
			c.Header("Retry-After", "30")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"msg":     "too many requests, retry later",
			})
			return
		}
		c.Next()
	}
}

package middleware

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// ipRateLimiter supports both Redis-backed (multi-instance safe) and
// in-memory (single-instance fallback) rate limiting per client IP.
type ipRateLimiter struct {
	mu       sync.RWMutex
	buckets  map[string]*rateBucket
	rate     float64 // tokens per second
	capacity float64 // bucket capacity
	rdb      *redis.Client
}

type rateBucket struct {
	tokens    float64
	lastCheck time.Time
}

// NewIPRateLimiter returns a Gin middleware that limits each IP to the given
// number of requests per second. If rdb is non-nil, uses Redis for distributed
// rate limiting (suitable for multi-instance deployment). Falls back to in-memory
// if Redis is unavailable.
func NewIPRateLimiter(ratePerSec int, rdb *redis.Client) gin.HandlerFunc {
	if ratePerSec <= 0 {
		ratePerSec = 10
	}
	limiter := &ipRateLimiter{
		buckets:  make(map[string]*rateBucket),
		rate:     float64(ratePerSec),
		capacity: float64(ratePerSec),
		rdb:      rdb,
	}
	// Start cleanup goroutine for in-memory fallback
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			limiter.mu.Lock()
			now := time.Now()
			for ip, b := range limiter.buckets {
				if now.Sub(b.lastCheck) > 10*time.Minute {
					delete(limiter.buckets, ip)
				}
			}
			limiter.mu.Unlock()
		}
	}()
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if limiter.rdb != nil {
			if !limiter.allowRedis(c.Request.Context(), ip) {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "Rate limit exceeded"}})
				return
			}
			c.Next()
			return
		}
		// In-memory fallback
		if !limiter.allowMemory(ip) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"message": "Rate limit exceeded"}})
			return
		}
		c.Next()
	}
}

// allowRedis uses a sliding window counter in Redis.
// Key: ratelimit:<ip>:<window> with TTL = 1 second window.
// On Redis failure, allows the request (fail-open).
func (l *ipRateLimiter) allowRedis(ctx context.Context, ip string) bool {
	now := time.Now()
	window := now.Unix()
	key := fmt.Sprintf("ratelimit:%s:%d", ip, window)

	count, err := l.rdb.Incr(ctx, key).Result()
	if err != nil {
		// Redis unavailable — fail open (allow request)
		return true
	}
	if count == 1 {
		// First request in this window — set expiry
		l.rdb.Expire(ctx, key, 2*time.Second)
	}
	return count <= int64(l.capacity)
}

// allowMemory is the original token bucket implementation for single-instance use.
func (l *ipRateLimiter) allowMemory(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[ip]
	if !ok {
		bucket = &rateBucket{tokens: l.capacity, lastCheck: time.Now()}
		l.buckets[ip] = bucket
	}
	now := time.Now()
	elapsed := now.Sub(bucket.lastCheck).Seconds()
	bucket.tokens = min(bucket.tokens+elapsed*l.rate, l.capacity)
	bucket.lastCheck = now
	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

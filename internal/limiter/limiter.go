// NOTE: the current implementation uses in-memory lock sharding. In a multi-pod environment, to avoid users bypassing the global quota, you want to replace this with a Redis + Lua script adapter.

// NB: no logs here (errors 429 are already handled by O11y())

package limiter

import (
	"context"
	"sync"
	"time"
)

// holds the GCRA state for a single IP
type RateLimiter struct {
	mu       sync.Mutex
	emission time.Duration
	burst    time.Duration
	tat      time.Time
	lastSeen time.Time
}

// computes the GCRA under a local lock
func (v *RateLimiter) allow() bool {
	// acquiring lock to read and write safely
	v.mu.Lock()
	defer v.mu.Unlock()

	now := time.Now()
	tat := v.tat

	if now.After(tat) {
		tat = now
	}

	newTat := tat.Add(v.emission)

	// rejecting the request if the new TAT exceeds the burst capacity
	if newTat.Sub(now) > v.burst {
		return false
	}

	v.tat = newTat
	v.lastSeen = now
	return true
}

// orchestrates rate limiting for individual IPs
type Manager struct {
	rateLimiters sync.Map
	rate         int
	burst        int
}

// initializes a new manager, calling the eviction background process with graceful shutdown
func NewManager(ctx context.Context, rate int, burst int) *Manager {
	m := &Manager{
		rate:  rate,
		burst: burst,
	}

	go m.cleanupWorker(ctx)
	return m
}

// evaluates if a request from the given IP can be allowed
func (m *Manager) Allow(ip string) bool {
	v, exists := m.rateLimiters.Load(ip)

	if !exists {
		emissionInt := time.Second / time.Duration(m.rate)
		now := time.Now()
		newRateLimiter := &RateLimiter{
			emission: emissionInt,
			burst:    emissionInt * time.Duration(m.burst),
			tat:      now,
			lastSeen: now,
		}

		// passes the pointer of the already initialized RateLimiter to each goroutine, preventing data-races if multiple threads create the same IP concurrently
		v, _ = m.rateLimiters.LoadOrStore(ip, newRateLimiter)
	}

	return v.(*RateLimiter).allow()
}

// Background daemon that eliminates IPs inactive for more than 5 minutes to free RAM
func (m *Manager) cleanupWorker(ctx context.Context) {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done(): // prevents goroutine leaks on server shutdown/tests
			return
		case <-ticker.C:
			now := time.Now()

			m.rateLimiters.Range(func(key, value interface{}) bool {
				v := value.(*RateLimiter)

				// acquiring lock to read safely (cfr. allow())
				v.mu.Lock()
				inactiveTime := now.Sub(v.lastSeen)
				if inactiveTime > 5*time.Minute {
					m.rateLimiters.Delete(key)
				}
				v.mu.Unlock()

				// keep checking for more inactive IPs
				return true
			})
		}
	}
}

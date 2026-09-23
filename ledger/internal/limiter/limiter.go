// TODO: Rate Limiter
// Current implementation uses in-memory lock sharding (sync.Map + sync.Mutex).
// In a multi-pod production environment behind a Load Balancer (K8s),
// this layer must be replaced with a Redis + Lua script adapter.
// The Lua script will guarantee distributed atomicity of the GCRA algorithm,
// neutralizing race conditions across API Gateway replicas.
package limiter

import (
	"sync"
	"time"
)

// visitor holds the GCRA state for a single IP.
type visitor struct {
	mu       sync.Mutex
	emission time.Duration
	burst    time.Duration
	tat      time.Time
	lastSeen time.Time
}

// allow computes the GCRA under a local lock.
func (v *visitor) allow() bool {
	v.mu.Lock()
	defer v.mu.Unlock()

	now := time.Now()
	tat := v.tat

	if now.After(tat) {
		tat = now
	}

	newTat := tat.Add(v.emission)

	// Reject request if the new TAT exceeds our burst capacity
	if newTat.Sub(now) > v.burst {
		return false
	}

	// Accept request and update state
	v.tat = newTat
	v.lastSeen = now
	return true
}

// Manager orchestrates rate limiting across multiple IPs.
type Manager struct {
	visitors sync.Map
	rate     int
	burst    int
}

// NewManager initializes a lock-sharded rate limiter and spawns its eviction daemon.
func NewManager(rate int, burst int) *Manager {
	m := &Manager{
		rate:  rate,
		burst: burst,
	}

	// Spawn background daemon to prevent Memory Leaks (OOM)
	go m.cleanupWorker()

	return m
}

// Allow evaluates if a request from the given IP is permitted.
func (m *Manager) Allow(ip string) bool {
	v, exists := m.visitors.Load(ip)

	if !exists {
		emissionInt := time.Second / time.Duration(m.rate)
		newVisitor := &visitor{
			emission: emissionInt,
			burst:    emissionInt * time.Duration(m.burst),
			tat:      time.Now(),
			lastSeen: time.Now(),
		}

		// LoadOrStore prevents data-races if multiple threads create the same IP simultaneously
		actualV, _ := m.visitors.LoadOrStore(ip, newVisitor)
		v = actualV
	}

	return v.(*visitor).allow()
}

// cleanupWorker is a background daemon that evicts stale IPs to free RAM.
func (m *Manager) cleanupWorker() {
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		now := time.Now()

		m.visitors.Range(func(key, value interface{}) bool {
			v := value.(*visitor)

			v.mu.Lock()
			inactiveTime := now.Sub(v.lastSeen)
			v.mu.Unlock()

			// Evict IPs unseen for more than 5 minutes
			if inactiveTime > 5*time.Minute {
				m.visitors.Delete(key)
			}

			return true
		})
	}
}

package limiter

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tests the sequential arithmetic of the GCRA algorithm
func TestManager_GCRA_Math(t *testing.T) {

	manager := NewManager(context.Background(), 5, 5)
	ip := "test_ip"

	for i := 0; i < 5; i++ {
		if !manager.Allow(ip) {
			t.Fatalf("Request %d incorrectly exceeded burst limit.", i+1)
		}
	}

	if manager.Allow(ip) {
		t.Fatalf("Burst limit not working.")
	}

	time.Sleep(210 * time.Millisecond)
	if !manager.Allow(ip) {
		t.Fatalf("TAT reset not working.")
	}
}

// tests absence of data-races on locks
func TestManager_Concurrency(t *testing.T) {

	manager := NewManager(context.Background(), 10, 10)
	targetIP := "123456"

	var successCount int32
	var wg sync.WaitGroup
	workers := 1000

	// triggering a Thundering Herd on the exact same IP at the exact same time
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			// ensuring task counter drops to zero, no matter what happens to each goroutine
			defer wg.Done()
			if manager.Allow(targetIP) {
				atomic.AddInt32(&successCount, 1)
			}
		}()
	}

	wg.Wait()

	// in general successCount >= manager.burst, but we want perfect equality
	if int(successCount) != manager.burst {
		t.Fatalf("Data-race detected. Expected %d successes, got %d.", manager.burst, successCount)
	}
}

// tests isolation of every bucket
func TestManager_MultipleIPs(t *testing.T) {

	manager := NewManager(context.Background(), 2, 2)
	ips := []string{"IP_1", "IP_2", "IP_3"}

	var wg sync.WaitGroup

	for _, ip := range ips {
		wg.Add(1)
		go func(clientIP string) {
			defer wg.Done()
			for i := 0; i < 2; i++ {
				if !manager.Allow(clientIP) {
					t.Errorf("IP %s was blocked earlier than expected.", clientIP)
				}
			}
		}(ip)
	}

	wg.Wait()
}

// tests concurrent throughput on a single bucket
func BenchmarkManager_Parallel(b *testing.B) {
	manager := NewManager(context.Background(), 1000, 1000)
	b.ResetTimer()

	// spawning goroutines across all available hardware threads
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			manager.Allow("192.168.1.1")
		}
	})
}

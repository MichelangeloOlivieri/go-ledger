package middleware

import (
	"net/http"
)

// interface for testing and decoupling
type RateLimiter interface {
	Allow(ip string) bool
}

// drops requests before CPU-heavy tasks
func RateLimit(limiter RateLimiter) func(http.Handler) http.Handler {
	return func(nextHandler http.Handler) http.Handler {

		limitHandler := func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow(r.RemoteAddr) {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			nextHandler.ServeHTTP(w, r)
		}
		return http.HandlerFunc(limitHandler)
	}
}

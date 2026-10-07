package api

import (
	"ledger/internal/middleware"
	"net/http"
)

// implements the router chaining the middleware functions
func NewRouter(api *APIServer, limiter middleware.RateLimiter, secret string) *http.ServeMux {
	mux := http.NewServeMux()

	// observability -> limiter -> auth -> handler
	creditHandler := http.HandlerFunc(api.HandleCredit)
	jwtMiddleware := middleware.JWTAuth([]byte(secret))
	rateLimitMiddleware := middleware.RateLimit(limiter)
	secureHandler := middleware.O11y(rateLimitMiddleware(jwtMiddleware(creditHandler)))

	mux.Handle("/credit", secureHandler)
	return mux
}

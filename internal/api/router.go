package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/httprate"
	"github.com/go-chi/jwtauth/v5"
)

type contextKey string

const userIDKey contextKey = "user_id"

// minimalist observability middleware
func timingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		duration := time.Since(start)

		slog.Info("Request completed",
			"path", r.URL.Path,
			"method", r.Method,
			"duration_ms", duration.Milliseconds(),
		)
	})
}

func userIDContext(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, claims, _ := jwtauth.FromContext(r.Context())
		sub, ok := claims["sub"].(string)
		if !ok {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), userIDKey, sub)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func NewRouter(api *APIServer, tokenAuth *jwtauth.JWTAuth) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	// business logic
	var transferHandler http.Handler = http.HandlerFunc(api.HandleTransfer)

	// bypass rate limiting during stress test
	if os.Getenv("STRESS_TEST") != "true" {
		transferHandler = httprate.LimitBy(
			10,
			1*time.Second,
			func(r *http.Request) (string, error) {
				val, ok := r.Context().Value(userIDKey).(string)
				if !ok || val == "" {
					return "", errors.New("critical security failure: identity missing in rate limiter")
				}
				return val, nil
			},
		)(transferHandler)
	}

	// inject user info into context
	awareHandler := userIDContext(transferHandler)

	// token validator
	securedHandler := jwtauth.Verifier(tokenAuth)(awareHandler)

	// observability
	monitoredHandler := timingMiddleware(securedHandler)

	mux.Handle("POST /transfer", monitoredHandler)
	return mux
}

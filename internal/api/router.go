package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/httprate"
	"github.com/go-chi/jwtauth/v5"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// NB: with 10 replicas of the same microservice, one would need to rethink the rate limiter and connection pooler and use external infrastructure (here omitted for simplicity)

type contextKey string

const userIDKey contextKey = "user_id"

func NewRouter(api *APIServer, tokenAuth *jwtauth.JWTAuth) *http.ServeMux {
	mux := http.NewServeMux()

	// business logic
	var transferHandler http.Handler = http.HandlerFunc(api.HandleTransfer)

	// bypassing rate limiter during stress test
	if os.Getenv("STRESS_TEST") != "true" {
		// rate limiter
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

	// user info into context
	awareHandler := userIDContext(transferHandler)
	// JWT validator
	securedHandler := jwtauth.Verifier(tokenAuth)(awareHandler)
	// OpenTelemetry
	otelHandler := otelhttp.NewHandler(securedHandler, "POST /transfer")

	mux.Handle("POST /transfer", otelHandler)
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	return mux
}

// ================================================================================================
// HELPER FUNCTIONS
// ================================================================================================

// inject validated user id into the context for the business logic
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

package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const UserIDKey contextKey = "user_id"

// TODO: secrets should not reside in the codebase, but it is being done like this
// for simplicity.
var jwtSecret = []byte("super-secret-key-tesseract-12345")

// JWTAuth parses the Authorization header, validates the token's cryptographic signature,
// and injects the extracted user ID into the request context.
func JWTAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error": "missing authorization header"}`, http.StatusUnauthorized)
			return
		}

		prefix, tokenString, found := strings.Cut(authHeader, " ")
		if !found || strings.ToLower(prefix) != "bearer" || tokenString == "" {
			http.Error(w, `{"error": "invalid token format"}`, http.StatusUnauthorized)
			return
		}

		token, err := jwt.Parse(tokenString, func(t *jwt.Token) (any, error) {
			_, ok := t.Method.(*jwt.SigningMethodHMAC)
			if !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return jwtSecret, nil
		})

		if err != nil || !token.Valid {
			http.Error(w, `{"error": "invalid or expired token"}`, http.StatusUnauthorized)
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			http.Error(w, `{"error": "invalid claims structure"}`, http.StatusUnauthorized)
			return
		}

		userID, _ := claims["sub"].(string)
		ctx := context.WithValue(r.Context(), UserIDKey, userID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

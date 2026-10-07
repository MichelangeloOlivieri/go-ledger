package middleware

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type ContextKey string

const UserIDKey ContextKey = "user_id"

// isolates validation logic
func validateToken(tokenString string, secret []byte) (int, error) {

	var claims jwt.RegisteredClaims

	// TECHNICAL: provides the secret key for cryptographic validation
	keyFunc := func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return secret, nil
	}

	token, err := jwt.ParseWithClaims(tokenString, &claims, keyFunc)
	if err != nil || !token.Valid {
		return 0, err
	}

	// parse from JWT standard string to strictly typed integer directly from the struct
	return strconv.Atoi(claims.Subject)
}

// parses the authorization header, extracts the user ID and injects it into the context (NB: no logs because O11y.go already takes care of that)
func JWTAuth(secret []byte) func(http.Handler) http.Handler {

	return func(nextHandler http.Handler) http.Handler {

		authHandler := func(w http.ResponseWriter, r *http.Request) {
			// parsing header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				// errors are to be returned to the previous handler (O11y)
				http.Error(w, `{"error": "missing authorization header"}`, http.StatusUnauthorized)
				return
			}

			// extracting token string
			prefix, tokenString, found := strings.Cut(authHeader, " ")
			if !found || strings.ToLower(prefix) != "bearer" || tokenString == "" {
				http.Error(w, `{"error": "invalid token format"}`, http.StatusUnauthorized)
				return
			}

			userID, err := validateToken(tokenString, secret)
			if err != nil || userID == 0 {
				http.Error(w, `{"error": "invalid or expired token"}`, http.StatusUnauthorized)
				return
			}

			// injecting user id and calling internal function
			ctx := context.WithValue(r.Context(), UserIDKey, userID)
			nextHandler.ServeHTTP(w, r.WithContext(ctx))
		}

		return http.HandlerFunc(authHandler)
	}
}

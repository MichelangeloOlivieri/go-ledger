package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TECHNICAL: helper to create JWTs for the test
func generateTestToken(userID string, secret []byte) string {
	claims := jwt.RegisteredClaims{
		Subject:   userID,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString(secret)
	return tokenString
}

// tests robustness against simple changes of id
func TestJWTAuth(t *testing.T) {
	testSecret := []byte("test-secret-ledger-key")

	// mocking the business logic
	mockCredit := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		val := r.Context().Value(UserIDKey)
		if val == nil {
			t.Errorf("Security Breach: Expected userID in context, got nil")
		}
		w.WriteHeader(http.StatusOK)
	})

	// injecting the secret via closure as per the new architectural design
	authHandler := JWTAuth(testSecret)(mockCredit)

	validToken := generateTestToken("123", testSecret)
	fakeToken := generateTestToken("999", []byte("wrong-attacker-password"))

	// Table-Driven Test covering all cases
	tests := []struct {
		name           string
		authHeader     string
		expectedStatus int
	}{
		{
			name:           "Missing Header",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Malformed Format",
			authHeader:     "Bearer",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Cryptographic Spoofing",
			authHeader:     "Bearer " + fakeToken,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Valid Token",
			authHeader:     "Bearer " + validToken,
			expectedStatus: http.StatusOK,
		},
	}

	// executing all four tests
	for _, test := range tests {
		testName := test.name
		testFunction := func(t *testing.T) {

			mockRequest := httptest.NewRequest(http.MethodPost, "/credit", nil)
			if test.authHeader != "" {
				mockRequest.Header.Set("Authorization", test.authHeader)
			}
			mockResponseWriter := httptest.NewRecorder()
			authHandler.ServeHTTP(mockResponseWriter, mockRequest)

			if mockResponseWriter.Code != test.expectedStatus {
				t.Fatalf("Expected status %d, got %d", test.expectedStatus, mockResponseWriter.Code)
			}
		}

		t.Run(testName, testFunction)
	}
}

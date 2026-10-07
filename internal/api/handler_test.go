package api

import (
	"bytes"
	"context"
	"errors"
	"ledger/internal/middleware"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mock WalletManager
type mockWallet struct {
	balance int64
	err     error
}

func (m *mockWallet) Credit(ctx context.Context, accountID int, amount int64, idempotencyKey string) (int64, error) {
	return m.balance, m.err
}

// tests HandleCredit in case of: success, malformed json, failed DB transaction, missing idempotency
func TestHandleCredit(t *testing.T) {
	tests := []struct {
		name           string
		walletBalance  int64
		walletErr      error
		payload        []byte
		idempotencyKey string
		expectedStatus int
	}{
		{
			name:           "Success - Valid transaction",
			walletBalance:  150,
			walletErr:      nil,
			payload:        []byte(`{"amount": 50}`),
			idempotencyKey: "test-key-123",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Invalid JSON Payload",
			walletBalance:  0,
			walletErr:      nil,
			payload:        []byte(`{"amount": "fifty"}`),
			idempotencyKey: "test-key-123",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Missing Idempotency Key",
			walletBalance:  0,
			walletErr:      nil,
			payload:        []byte(`{"amount": 100}`),
			idempotencyKey: "",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Domain Error - DB Constraint or Internal Failure",
			walletBalance:  0,
			walletErr:      errors.New("db transaction failed"),
			payload:        []byte(`{"amount": 100}`),
			idempotencyKey: "test-key-123",
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewAPIServer(
				&mockWallet{balance: tt.walletBalance, err: tt.walletErr},
			)

			req := httptest.NewRequest(http.MethodPost, "/credit", bytes.NewReader(tt.payload))
			req.RemoteAddr = "192.168.1.100:12345"

			if tt.idempotencyKey != "" {
				req.Header.Set("Idempotency-Key", tt.idempotencyKey)
			}

			ctx := context.WithValue(req.Context(), middleware.UserIDKey, 1)
			req = req.WithContext(ctx)
			rec := httptest.NewRecorder()
			server.HandleCredit(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d; got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}

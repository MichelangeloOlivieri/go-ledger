package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ledger/internal/wallet"
)

type mockWallet struct {
	err error
}

func (m *mockWallet) Transfer(ctx context.Context, fromID int64, toID int64, amount int64, idempotencyKey string) error {
	return m.err
}

func TestHandleTransfer(t *testing.T) {
	tests := []struct {
		name           string
		walletErr      error
		payload        []byte
		idempotencyKey string
		injectIdentity string // simulates validated JWT
		expectedStatus int
	}{
		{
			name:           "Success - Valid transaction (Zero-Trust Auth)",
			walletErr:      nil,
			payload:        []byte(`{"to_account_id": 2, "amount": 50}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Security - IDOR Exploit Prevented (Missing Identity Context)",
			walletErr:      nil,
			payload:        []byte(`{"to_account_id": 2, "amount": 50}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Security - Malformed Subject Claim",
			walletErr:      nil,
			payload:        []byte(`{"to_account_id": 2, "amount": 50}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "invalid-uuid",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Domain Error - Insufficient Funds",
			walletErr:      wallet.ErrInsufficientFunds,
			payload:        []byte(`{"to_account_id": 2, "amount": 100}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusUnprocessableEntity,
		},
		{
			name:           "Domain Error - Account Not Found",
			walletErr:      wallet.ErrAccountNotFound,
			payload:        []byte(`{"to_account_id": 2, "amount": 100}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "Domain Error - DB Constraint",
			walletErr:      errors.New("db transaction failed"),
			payload:        []byte(`{"to_account_id": 2, "amount": 100}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:           "Edge Defense - Self Transfer",
			walletErr:      nil,
			payload:        []byte(`{"to_account_id": 1, "amount": 50}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Edge Defense - Malformed JSON",
			walletErr:      nil,
			payload:        []byte(`{"amount": "fifty"}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Edge Defense - Negative Amount",
			walletErr:      nil,
			payload:        []byte(`{"to_account_id": 2, "amount": -10}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Edge Defense - OOM JSON Bomb (Payload > 10KB)",
			walletErr:      nil,
			payload:        []byte(`{"to_account_id": 2, "amount": 50, "pad": "` + strings.Repeat("x", 11000) + `"}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewAPIServer(&mockWallet{err: tt.walletErr})
			req := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewReader(tt.payload))

			if tt.idempotencyKey != "" {
				req.Header.Set("Idempotency-Key", tt.idempotencyKey)
			}

			if tt.injectIdentity != "" {
				ctx := context.WithValue(req.Context(), userIDKey, tt.injectIdentity)
				req = req.WithContext(ctx)
			}

			rec := httptest.NewRecorder()
			server.HandleTransfer(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d; got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}

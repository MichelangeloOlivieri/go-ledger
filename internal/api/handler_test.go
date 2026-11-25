package api

import (
	"bytes"
	"context"
	"errors"
	"io"
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
			name:           "Valid transaction",
			walletErr:      nil,
			payload:        []byte(`{"to_id": 2, "amount": 50}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "Edge Defense - Payload > 10KB",
			walletErr:      nil,
			payload:        []byte(`{"to_id": 2, "amount": 50, "pad": "` + strings.Repeat("x", 11000) + `"}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Edge Defense - Malformed JSON",
			walletErr:      nil,
			payload:        []byte(`{"amount": "a ton of money"}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Security - Missing Identity Context",
			walletErr:      nil,
			payload:        []byte(`{"to_id": 2, "amount": 50}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "Security - Malformed Subject Claim",
			walletErr:      nil,
			payload:        []byte(`{"to_id": 2, "amount": 50}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "some-invalid-uuid",
			expectedStatus: http.StatusBadRequest,
		},

		{
			name:           "Edge Defense - Self Transfer",
			walletErr:      nil,
			payload:        []byte(`{"to_id": 1, "amount": 50}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Edge Defense - Negative Amount",
			walletErr:      nil,
			payload:        []byte(`{"to_id": 2, "amount": -10}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Edge Defense - Missing Idempotency Key",
			walletErr:      nil,
			payload:        []byte(`{"to_id": 2, "amount": 50}`),
			idempotencyKey: "",
			injectIdentity: "1",
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "Domain Error - Idempotency Mismatch", // other ones are analogous
			walletErr:      wallet.ErrIdempotencyMismatch,
			payload:        []byte(`{"to_id": 2, "amount": 50}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusUnprocessableEntity,
		},
		{
			name:           "Domain Error - Internal Failure",
			walletErr:      errors.New("server.Transfer() returns a non-nil error"),
			payload:        []byte(`{"to_id": 13, "amount": 50}`),
			idempotencyKey: "test-key-123",
			injectIdentity: "1",
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/transfers", bytes.NewReader(tt.payload))

			if tt.idempotencyKey != "" {
				req.Header.Set("Idempotency-Key", tt.idempotencyKey)
			}

			if tt.injectIdentity != "" {
				ctx := context.WithValue(req.Context(), userIDKey, tt.injectIdentity)
				req = req.WithContext(ctx)
			}

			server := NewAPIServer(&mockWallet{err: tt.walletErr})
			server.HandleTransfer(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d; got %d, body: %s", tt.expectedStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func BenchmarkHandleTransfer(b *testing.B) {
	server := NewAPIServer(&mockWallet{err: nil})
	payload := []byte(`{"to_id": 2, "amount": 50}`)

	req := httptest.NewRequest(http.MethodPost, "/transfer", nil)
	req.Header.Set("Idempotency-Key", "bench-key")

	ctx := context.WithValue(req.Context(), userIDKey, "1")
	req = req.WithContext(ctx)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		req.Body = http.NoBody
		req.Body = io.NopCloser(bytes.NewReader(payload))
		rec := httptest.NewRecorder()

		server.HandleTransfer(rec, req)
	}
}

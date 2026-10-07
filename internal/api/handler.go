package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"ledger/internal/middleware"
)

type CreditRequest struct {
	Amount int64 `json:"amount"` // No account_id exposed to the client
}

// interface for testing
type WalletManager interface {
	Credit(ctx context.Context, accountID int, amount int64, idempotencyKey string) (int64, error)
}

type APIServer struct {
	walletService WalletManager
}

func NewAPIServer(ws WalletManager) *APIServer {
	return &APIServer{
		walletService: ws,
	}
}

func (s *APIServer) HandleCredit(w http.ResponseWriter, r *http.Request) {
	var req CreditRequest
	err := json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	if req.Amount <= 0 {
		http.Error(w, "Credit amount must be strictly positive", http.StatusBadRequest)
		return
	}

	// extracts the cryptographically secured identity from the JWT middleware context
	accountID, ok := r.Context().Value(middleware.UserIDKey).(int)
	if !ok {
		http.Error(w, "Identity missing from context", http.StatusInternalServerError)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		http.Error(w, "Idempotency-Key missing", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	// executes business logic
	newBalance, err := s.walletService.Credit(ctx, accountID, req.Amount, idempotencyKey)
	if err != nil {
		// logs the real error internally for debugging and O11y
		slog.ErrorContext(ctx, "Transaction failed", "wallet_id", accountID, "error", err)
		// providing exact error info
		http.Error(w, `{"error": "internal_server_error"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "Credit transaction successful. New Balance: %d\n", newBalance)
}

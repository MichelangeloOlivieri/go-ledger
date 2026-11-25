package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"encoding/json"

	"ledger/internal/wallet"
)

// errors global variables
var (
	respInvalidJSON         = []byte(`{"error": "invalid_json_payload"}`)
	respUnauthorized        = []byte(`{"error": "unauthorized_identity_missing"}`)
	respMalformedIdentity   = []byte(`{"error": "malformed_identity_format"}`)
	respInvalidAmount       = []byte(`{"error": "transfer_amount_must_be_strictly_positive"}`)
	respSelfTransfer        = []byte(`{"error": "self_transfer_not_allowed"}`)
	respMissingIdempotency  = []byte(`{"error": "idempotency_key_missing"}`)
	respIdempotencyMismatch = []byte(`{"error": "idempotency_mismatch"}`)
	respInsufficientFunds   = []byte(`{"error": "insufficient_funds"}`)
	respAccountNotFound     = []byte(`{"error": "account_not_found"}`)
	respTransactionTimeout  = []byte(`{"error": "transaction_timeout"}`)
	respInternalError       = []byte(`{"error": "internal_server_error"}`)
	respSuccess             = []byte(`{"status": "success", "message": "transfer transaction successful"}`)
)

type TransferRequest struct {
	ToId   int64 `json:"to_id"`
	Amount int64 `json:"amount"`
}

type WalletManager interface {
	Transfer(ctx context.Context, fromID int64, toID int64, amount int64, idempotencyKey string) error
}

type APIServer struct {
	walletService WalletManager
}

func NewAPIServer(ws WalletManager) *APIServer {
	return &APIServer{walletService: ws}
}

func (s *APIServer) HandleTransfer(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10240)
	defer func() {
		if err := r.Body.Close(); err != nil {
			slog.Warn("failed to close request body", "error", err)
		}
	}()

	// sender identity extraction
	userIDStr, ok := r.Context().Value(userIDKey).(string)
	if !ok || userIDStr == "" {
		writeJSON(w, http.StatusUnauthorized, respUnauthorized)
		return
	}

	fromId, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, respMalformedIdentity)
		return
	}

	// JSON parsing: receiver identity and amount extraction
	var req TransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, respInvalidJSON)
		return
	}

	toId := req.ToId
	amount := req.Amount

	// domain validations
	if fromId == toId {
		writeJSON(w, http.StatusBadRequest, respSelfTransfer)
		return
	}

	if amount <= 0 {
		writeJSON(w, http.StatusBadRequest, respInvalidAmount)
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		writeJSON(w, http.StatusBadRequest, respMissingIdempotency)
		return
	}

	// business logic
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := s.walletService.Transfer(ctx, fromId, toId, amount, idempotencyKey); err != nil {

		slog.ErrorContext(ctx, "transaction failed", "from", fromId, "to", req.ToId, "error", err)

		switch {
		case errors.Is(err, wallet.ErrIdempotencyMismatch):
			writeJSON(w, http.StatusUnprocessableEntity, respIdempotencyMismatch)
		case errors.Is(err, wallet.ErrInsufficientFunds):
			writeJSON(w, http.StatusUnprocessableEntity, respInsufficientFunds)
		case errors.Is(err, wallet.ErrAccountNotFound):
			writeJSON(w, http.StatusNotFound, respAccountNotFound)
		case errors.Is(err, context.DeadlineExceeded):
			writeJSON(w, http.StatusGatewayTimeout, respTransactionTimeout)
		default:
			writeJSON(w, http.StatusInternalServerError, respInternalError)
		}
		return
	}

	writeJSON(w, http.StatusOK, respSuccess)
}

// ================================================================================================
// HELPER FUNCTIONS
// ================================================================================================

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

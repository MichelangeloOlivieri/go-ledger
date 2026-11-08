package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"ledger/internal/wallet"
)

type TransferRequest struct {
	ToAccountID int64 `json:"to_account_id"`
	Amount      int64 `json:"amount"`
}

type WalletManager interface {
	Transfer(ctx context.Context, fromID, toID int64, amount int64, idempotencyKey string) error
}

type APIServer struct {
	walletService WalletManager
}

func NewAPIServer(ws WalletManager) *APIServer {
	return &APIServer{walletService: ws}
}

var (
	respInvalidJSON        = []byte(`{"error": "invalid_json_payload"}`)
	respUnauthorized       = []byte(`{"error": "unauthorized_identity_missing"}`)
	respMalformedIdentity  = []byte(`{"error": "malformed_identity_format"}`)
	respInvalidAmount      = []byte(`{"error": "transfer_amount_must_be_strictly_positive"}`)
	respSelfTransfer       = []byte(`{"error": "self_transfer_not_allowed"}`)
	respMissingIdempotency = []byte(`{"error": "idempotency_key_missing"}`)
	respInternalError      = []byte(`{"error": "internal_server_error"}`)
	respSuccess            = []byte(`{"status": "success", "message": "transfer transaction successful"}`)
)

func writeJSON(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (s *APIServer) HandleTransfer(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 10240)
	defer r.Body.Close()

	var req TransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, respInvalidJSON)
		return
	}

	// identity extraction
	userIDStr, ok := r.Context().Value(userIDKey).(string)
	if !ok || userIDStr == "" {
		writeJSON(w, http.StatusUnauthorized, respUnauthorized)
		return
	}

	fromAccountID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, respMalformedIdentity)
		return
	}

	// domain validations
	if req.Amount <= 0 {
		writeJSON(w, http.StatusBadRequest, respInvalidAmount)
		return
	}

	if fromAccountID == req.ToAccountID {
		writeJSON(w, http.StatusBadRequest, respSelfTransfer)
		return
	}

	// idempotency check
	idempotencyKey := r.Header.Get("Idempotency-Key")
	if idempotencyKey == "" {
		writeJSON(w, http.StatusBadRequest, respMissingIdempotency)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	// business logic
	if err := s.walletService.Transfer(ctx, fromAccountID, req.ToAccountID, req.Amount, idempotencyKey); err != nil {
		slog.ErrorContext(ctx, "Transaction failed", "from", fromAccountID, "to", req.ToAccountID, "error", err)

		switch {
		case errors.Is(err, wallet.ErrInsufficientFunds):
			writeJSON(w, http.StatusUnprocessableEntity, []byte(`{"error": "insufficient_funds"}`))
		case errors.Is(err, wallet.ErrAccountNotFound):
			writeJSON(w, http.StatusNotFound, []byte(`{"error": "account_not_found"}`))
		case errors.Is(err, context.DeadlineExceeded):
			writeJSON(w, http.StatusGatewayTimeout, []byte(`{"error": "timeout_processing_transaction"}`))
		default:
			writeJSON(w, http.StatusInternalServerError, respInternalError)
		}
		return
	}

	writeJSON(w, http.StatusOK, respSuccess)
}

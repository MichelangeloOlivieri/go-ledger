package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/jwtauth/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ledger/internal/api"
	"ledger/internal/broker"
	"ledger/internal/wallet"
	"ledger/internal/worker"
)

func main() {
	dbUrl := os.Getenv("DATABASE_URL")
	if dbUrl == "" {
		dbUrl = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "HSsuper-secret-ledger-key-123456" // Default solo per test locali
	}

	ctx := context.Background()

	// API Pool: scales for high concurrency, tolerates blocking locks
	apiCfg, err := pgxpool.ParseConfig(dbUrl)
	if err != nil {
		slog.Error("Failed to parse database URL", "error", err)
		os.Exit(1)
	}
	apiCfg.MaxConns = 80

	apiPool, err := pgxpool.NewWithConfig(ctx, apiCfg)
	if err != nil {
		slog.Error("API Database connection failed", "error", err)
		os.Exit(1)
	}
	defer apiPool.Close()

	// Worker Pool: isolated resources ensuring constant CDC polling throughput
	workerCfg, _ := pgxpool.ParseConfig(dbUrl)
	workerCfg.MaxConns = 20

	workerPool, err := pgxpool.NewWithConfig(ctx, workerCfg)
	if err != nil {
		slog.Error("Worker Database connection failed", "error", err)
		os.Exit(1)
	}
	defer workerPool.Close()

	eventPublisher := broker.NewConsolePublisher()
	outboxWorker := worker.NewOutboxWorker(workerPool, eventPublisher)
	workerCtx, workerCancel := context.WithCancel(ctx)
	var workerWg sync.WaitGroup

	workerWg.Add(1)
	go func() {
		defer workerWg.Done()
		outboxWorker.Start(workerCtx)
	}()

	// Edge setup
	walletService := wallet.NewWalletService(apiPool)
	apiServer := api.NewAPIServer(walletService)
	tokenAuth := jwtauth.New("HS256", []byte(jwtSecret), nil)
	mux := api.NewRouter(apiServer, tokenAuth)

	// SOTA: strict timeouts to mitigate Slowloris attacks
	srv := &http.Server{
		Addr:         ":8000",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	go func() {
		slog.Info("Listening on port 8000...")
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			slog.Error("Critical server error", "error", err)
			os.Exit(1)
		}
	}()

	// Graceful shutdown coordination
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("Initiating shutdown...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// SOTA: Cancel the worker context IMMEDIATELY alongside the HTTP shutdown.
	// This ensures the worker finishes its current transaction (via SKIP LOCKED)
	// without starting new ones, preventing zombie events or race conditions.
	workerCancel()

	// Wait for the HTTP server to finish active requests
	if err = srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Forced HTTP server shutdown", "error", err)
	}

	workerDone := make(chan struct{})
	go func() {
		workerWg.Wait()
		close(workerDone)
	}()

	// Wait for the worker to finish its active DB transaction
	select {
	case <-workerDone:
		slog.Info("CDC Worker cleanly stopped.")
	case <-shutdownCtx.Done():
		slog.Error("Timeout exceeded: Forced CDC Worker shutdown")
	}

	slog.Info("Server exited cleanly.")
}

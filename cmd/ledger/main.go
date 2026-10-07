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

	"github.com/jackc/pgx/v5/pgxpool"

	"ledger/internal/api"
	"ledger/internal/broker"
	"ledger/internal/db"
	"ledger/internal/limiter"
	"ledger/internal/wallet"
	"ledger/internal/worker"
)

func main() {
	// db configuration
	dbUrl := os.Getenv("DATABASE_URL")
	if dbUrl == "" {
		dbUrl = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}

	// migration for simplicity
	db.RunMigrations("file://migrations", dbUrl)
	ctx := context.Background()

	// configuring connection pool
	dbConfig, err := pgxpool.ParseConfig(dbUrl)
	if err != nil {
		slog.Error("Failed to parse database URL", "error", err)
		os.Exit(1)
	}
	dbConfig.MaxConns = 100

	// connecting to db
	pool, err := pgxpool.NewWithConfig(ctx, dbConfig)
	if err != nil {
		slog.Error("Database connection failed", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	// background workers
	eventPublisher := broker.NewConsolePublisher()
	outboxWorker := worker.NewOutboxWorker(pool, eventPublisher)
	workerCtx, workerCancel := context.WithCancel(ctx)
	var workerWg sync.WaitGroup

	workerWg.Add(1)
	go func() {
		defer workerWg.Done()
		outboxWorker.Start(workerCtx)
	}()

	// mux setup
	walletService := wallet.NewWalletService(pool)
	rateLimiter := limiter.NewManager(ctx, 1000, 1000)
	apiServer := api.NewAPIServer(walletService)

	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "super-secret-ledger-key-12345"
	}
	mux := api.NewRouter(apiServer, rateLimiter, secret)

	// starting server
	srv := &http.Server{
		Addr:    ":8000",
		Handler: mux,
	}

	go func() {
		slog.Info("Ledger API Gateway listening on port 8000...")
		err := srv.ListenAndServe()
		if err != nil && err != http.ErrServerClosed {
			slog.Error("Critical server error", "error", err)
			os.Exit(1)
		}
	}()

	// graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("Initiating shutdown...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = srv.Shutdown(shutdownCtx)
	if err != nil {
		slog.Error("Forced shutdown", "error", err)
	}

	workerCancel()
	workerDone := make(chan struct{})
	go func() {
		workerWg.Wait()
		close(workerDone)
	}()

	select {
	case <-workerDone:
	case <-shutdownCtx.Done():
		slog.Error("Timeout exceeded: Forced CDC Worker shutdown")
		os.Exit(1)
	}

	slog.Info("Server exited cleanly.")
}

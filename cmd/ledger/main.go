package main

import (
	"context"
	"log/slog"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/go-chi/jwtauth/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"ledger/internal/api"
	"ledger/internal/broker"
	"ledger/internal/telemetry"
	"ledger/internal/wallet"
	"ledger/internal/worker"
)

func main() {
	go func() {
		_ = http.ListenAndServe("localhost:6060", nil)
	}()

	dbUrl := os.Getenv("DATABASE_URL")
	if dbUrl == "" {
		dbUrl = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}

	jwtSecret := os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = "HSsuper-secret-ledger-key-123456"
	}

	ctx := context.Background()

	tp, err := telemetry.InitTracer(ctx, "ledger-api")
	if err != nil {
		slog.Error("Failed to initialize OTel", "error", err)
	} else if tp != nil {
		defer func() { _ = tp.Shutdown(ctx) }()
	}

	// API pool
	apiCfg, err := pgxpool.ParseConfig(dbUrl)
	if err != nil {
		slog.Error("Failed to parse database URL", "error", err)
		os.Exit(1)
	}
	apiCfg.MaxConns = 25
	apiCfg.MinConns = 5
	apiCfg.MaxConnLifetime = 30 * time.Minute
	apiCfg.MaxConnIdleTime = 5 * time.Minute
	apiCfg.ConnConfig.Tracer = otelpgx.NewTracer()

	apiPool, err := pgxpool.NewWithConfig(ctx, apiCfg)
	if err != nil {
		slog.Error("API Database connection failed", "error", err)
		os.Exit(1)
	}
	defer apiPool.Close()

	// worker pool
	workerCfg, _ := pgxpool.ParseConfig(dbUrl)
	workerCfg.MaxConns = 2
	workerCfg.MinConns = 1
	workerCfg.MaxConnLifetime = 30 * time.Minute
	workerCfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "15000"
	workerCfg.ConnConfig.Tracer = otelpgx.NewTracer()

	workerPool, err := pgxpool.NewWithConfig(ctx, workerCfg)
	if err != nil {
		slog.Error("Worker Database connection failed", "error", err)
		os.Exit(1)
	}
	defer workerPool.Close()

	// publisher and worker (NB: publisher needs to be thread-safe for the worker to function)
	eventPublisher := broker.NewConsolePublisher()
	outboxWorker := worker.NewOutboxWorker(workerPool, eventPublisher)
	workerCtx, workerCancel := context.WithCancel(ctx)
	var workerWg sync.WaitGroup

	workerWg.Add(1)
	go func() {
		defer workerWg.Done()
		outboxWorker.Start(workerCtx)
	}()

	// setup: business logic, middlewares chain and multiplexer
	walletService := wallet.NewWalletService(apiPool)
	apiServer := api.NewAPIServer(walletService)
	tokenAuth := jwtauth.New("HS256", []byte(jwtSecret), nil)
	mux := api.NewRouter(apiServer, tokenAuth)

	srv := &http.Server{
		Addr:         ":8000",
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  15 * time.Second,
	}

	// start the server
	go func() {
		slog.Info("Listening on port 8000...")
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
	workerCancel()

	if err = srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("Forced HTTP server shutdown", "error", err)
	}

	workerDone := make(chan struct{})
	go func() {
		workerWg.Wait()
		close(workerDone)
	}()

	select {
	case <-workerDone:
		slog.Info("Polling worker cleanly stopped")
	case <-shutdownCtx.Done():
		slog.Error("Timeout exceeded: forced polling worker shutdown")
	}

	slog.Info("Server exited cleanly.")
}

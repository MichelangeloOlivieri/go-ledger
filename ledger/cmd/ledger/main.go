package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"tesseract/internal/limiter"
	"tesseract/internal/middleware"
	"tesseract/internal/wallet"
	"tesseract/internal/worker"
)

type CreditRequest struct {
	Amount int64 `json:"amount"`
}

// WalletManager defines the contract for domain-level financial operations.
type WalletManager interface {
	Credit(ctx context.Context, amount int64) (int64, error)
}

// RateLimiter defines the contract for IP-based request throttling.
type RateLimiter interface {
	Allow(ip string) bool
}

// APIServer orchestrates HTTP transport.
type APIServer struct {
	walletService WalletManager
	limiter       RateLimiter
}

// TODO: Implement Idempotency Keys.
// To prevent double-spending during client network retries, the API should require
// an 'Idempotency-Key' (UUID) header. The value will be cached (Redis), and
// duplicate requests within 24h should return the cached response (or HTTP 409).
func (s *APIServer) HandleCredit(w http.ResponseWriter, r *http.Request) {
	ip := r.RemoteAddr
	if !s.limiter.Allow(ip) {
		http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	var req CreditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	newBalance, err := s.walletService.Credit(r.Context(), req.Amount)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "Credit transaction successful (OCC). New Balance: %d\n", newBalance)
}

// initTracer bootstraps the OpenTelemetry provider for distributed tracing.
func initTracer(ctx context.Context) (*sdktrace.TracerProvider, error) {
	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithInsecure())
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	return tp, nil
}

// runDBMigration executes up-migrations to ensure the schema matches the application version.
func runDBMigration(migrationURL string, dbSource string) {
	migration, err := migrate.New(migrationURL, dbSource)
	if err != nil {
		log.Fatalf("Failed to create migration instance: %v", err)
	}

	if err := migration.Up(); err != nil && err != migrate.ErrNoChange {
		log.Fatalf("Failed to run up-migrations: %v", err)
	}
	log.Println("Database migrations applied successfully.")
}

func main() {
	dbUrl := os.Getenv("DATABASE_URL")
	if dbUrl == "" {
		dbUrl = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}
	runDBMigration("file://migrations", dbUrl)
	ctx := context.Background()

	tp, err := initTracer(ctx)
	if err != nil {
		log.Fatalf("Failed to initialize OpenTelemetry tracer: %v", err)
	}
	defer tp.Shutdown(ctx)

	pool, err := pgxpool.New(ctx, dbUrl)
	if err != nil {
		log.Fatalf("Database connection failed: %v", err)
	}
	defer pool.Close()

	outboxWorker := worker.NewOutboxWorker(pool)
	workerCtx, workerCancel := context.WithCancel(ctx)
	go outboxWorker.Start(workerCtx)

	walletService := wallet.NewWalletService(pool)
	rateLimiter := limiter.NewManager(10, 5)

	api := &APIServer{
		walletService: walletService,
		limiter:       rateLimiter,
	}

	// TODO: Replace REST/JSON transport with gRPC and Protobuf.
	mux := http.NewServeMux()

	// Chaining standard middleware: Observability -> Auth -> Handler
	creditHandler := http.HandlerFunc(api.HandleCredit)
	secureHandler := middleware.O11y(middleware.JWTAuth(creditHandler))
	mux.Handle("/credit", secureHandler)

	// Liveness Probe for Kubernetes
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	srv := &http.Server{
		Addr:    ":8000",
		Handler: mux,
	}

	go func() {
		log.Println("Tesseract API Gateway listening on port 8000...")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Critical server error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("\nInitiating graceful shutdown...")

	workerCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Forced shutdown: %v", err)
	}

	log.Println("Server exited cleanly.")
}

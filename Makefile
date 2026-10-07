.PHONY: test coverage profile-cpu lint build docker-build stress clean

# --- TESTING & QUALITY ---

# tests for data-races
test:
	go test -v -race ./...

# uses the linter
lint:
	golangci-lint run ./...

# verifies code coverage and generates a visual heat-map report
coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html
	@go tool cover -func=coverage.out | grep total

# testing cpu-performance to find bottlenecks
profile-cpu:
	go test -cpuprofile=cpu.prof ./internal/wallet/...
	go tool pprof -http=:8080 cpu.prof

# --- BUILD & DEPLOY ---

# compiles the code
build:
	go build -o ledger-api cmd/ledger/main.go

# creates the image with the dockerfile
docker-build:
	docker build -t ledger-api:latest .

# --- STRESS TESTING ---

# default database connection string
DOCKER_DB_URL ?= "postgres://postgres:postgres@host.docker.internal:5432/postgres?sslmode=disable"

# injects test data, runs k6 via docker, and guarantees database cleanup afterwards
stress:
	@echo "=> [1/3] Seeding test data..."
	@docker run --rm -i -v "$(PWD):/app" postgres:15-alpine psql $(DOCKER_DB_URL) -f /app/scripts/seed.sql
	@echo "=> [2/3] Running load test with k6..."
	@docker run --rm -i -v "$(PWD):/app" -w /app grafana/k6 run load_test.js; \
	K6_EXIT=$$?; \
	echo "=> [3/3] Tearing down test data..."; \
	docker run --rm -i -v "$(PWD):/app" postgres:15-alpine psql $(DOCKER_DB_URL) -f /app/scripts/teardown.sql; \
	exit $$K6_EXIT

# --- UTILS ---

# cleans directory
clean:
	rm -f ledger-api coverage.out coverage.html cpu.prof
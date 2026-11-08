.PHONY: test lint coverage stress

test:
	go test -v -race ./...

lint:
	docker run --rm -v "$(CURDIR):/app" -w /app golangci/golangci-lint:latest golangci-lint run ./...

coverage:
	go test -coverprofile=coverage.out ./...
# findstr instead of grep, otherwise won't work
	go tool cover -func=coverage.out | findstr total

stress:
	@echo "=> [0/5] Security Teardown..."
	@docker compose down -v --remove-orphans

	@echo "=> [1/5] Dynamic JWT seed generation..."
	@go run scripts/gen_jwt.go

	@echo "=> [2/5] Build, start and wait for infrastructure health..."
	docker compose up -d --wait --build postgres api
	
	@echo "=> [3/5] Seeding DB data..."
	docker compose exec -T postgres psql -U postgres -d postgres < scripts/seed.sql
	
	@echo "=> [4/5] Executing Load Test..."
	docker compose run --rm k6 run /app/load_test.js
	
	@echo "=> [5/5] Teardown and clean up..."
	@docker compose down -v --remove-orphans
	@-rm -f seed_users.json 2>nul || del /f seed_users.json 2>nul || true
# Stage 1: Builder
FROM golang:1.22-alpine AS builder

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o ledger-api cmd/ledger/main.go

# Create a non-root user for secure execution in the final image
RUN adduser -D -g '' appuser

# Stage 2: Production Release
FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Import the non-root user profile to prevent Privilege Escalation
COPY --from=builder /etc/passwd /etc/passwd

COPY --from=builder /build/ledger-api /ledger-api

USER appuser

EXPOSE 8000

ENTRYPOINT ["/ledger-api"]
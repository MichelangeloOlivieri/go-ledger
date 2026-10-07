# Stage 1: Builder

# provisions a lightweight Linux base image with a pre-installed Go compiler
FROM golang:1.22-alpine AS builder

# disables Go's bindings to C-libraries and makes the compiler independent of the host hardware
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

# creates and sets the absolute working directory for subsequent commands
WORKDIR /build

# copies the dependency manifests
COPY go.mod go.sum ./

# downloads said dependencies from the internet into the container's internal cache
RUN go mod download

# copies the application source code
COPY . .

# compiles said source code into an executable named 'ledger-api'
RUN go build -o ledger-api cmd/ledger/main.go

# creates a non-root user for secure execution in the final image
RUN adduser -D -g '' appuser

# Stage 2: Production Release

# initializes a completely empty image
FROM scratch

# imports: root certificates for external HTTPS calls, non-root user profile, compiled standalone binary 
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /build/ledger-api /ledger-api
COPY --from=builder /build/migrations /migrations

# delegates execution to the de-privileged user
USER appuser

# documents the port mapping
EXPOSE 8000

# binds the container existence to the execution of the binary (PID 1)
ENTRYPOINT ["/ledger-api"]
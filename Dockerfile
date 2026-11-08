# Stage 1: Builder

# provisions a lightweight Linux base image with a pre-installed Go compiler
FROM golang:1.26-alpine AS builder

# install tzdata to package timezone definitions for the scratch image
RUN apk add --no-cache tzdata

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

# ARCHITECTURAL NOTE: -s -w strips debugging info to reduce binary size and memory footprint.
# -trimpath ensures identical binary checksums across different CI environments.
RUN go build -trimpath -ldflags="-s -w" -o ledger-api cmd/ledger/main.go

# creates a non-root user for secure execution in the final image
RUN adduser -D -g '' appuser

# Stage 2: Production Release

# initializes a completely empty image
FROM scratch

# imports: root certificates for external HTTPS calls, timezone data, non-root user profile, compiled standalone binary 
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /build/ledger-api /ledger-api

# delegates execution to the de-privileged user
USER appuser

# documents the port mapping
EXPOSE 8000

# binds the container existence to the execution of the binary (PID 1)
ENTRYPOINT ["/ledger-api"]
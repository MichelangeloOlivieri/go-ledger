# stage 1: build

FROM golang:1.26-alpine AS builder

RUN apk add --no-cache tzdata

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /build

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN go build -trimpath -ldflags="-s -w" -o ledger-api cmd/ledger/main.go

RUN adduser -D -g '' appuser

# stage 2: release

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /build/ledger-api /ledger-api

USER appuser

EXPOSE 8000

ENTRYPOINT ["/ledger-api"]
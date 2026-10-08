# Multi-stage build for netbackup
FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY . .

RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /app/netbackup ./cmd/netbackup

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /app/netbackup /app/netbackup

ENTRYPOINT ["/app/netbackup"]

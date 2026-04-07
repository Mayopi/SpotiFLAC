# Build stage
FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/spotiflac-api ./cmd/api/

# Runtime stage
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -S spotiflac && adduser -S spotiflac -G spotiflac

WORKDIR /app

COPY --from=builder /bin/spotiflac-api /app/spotiflac-api

RUN mkdir -p /downloads && chown spotiflac:spotiflac /downloads

USER spotiflac

ENV PORT=8080
ENV OUTPUT_DIR=/downloads

EXPOSE 8080

VOLUME ["/downloads"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["/app/spotiflac-api"]

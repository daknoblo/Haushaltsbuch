# syntax=docker/dockerfile:1

# ---- Build stage ----
ARG GO_VERSION=1.26
FROM golang:${GO_VERSION}-alpine AS builder
RUN apk add --no-cache ca-certificates git
WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
ARG CHANNEL=local
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w \
      -X github.com/daknoblo/Haushaltsbuch/internal/version.Version=${VERSION} \
      -X github.com/daknoblo/Haushaltsbuch/internal/version.Channel=${CHANNEL} \
      -X github.com/daknoblo/Haushaltsbuch/internal/version.Commit=${COMMIT} \
      -X github.com/daknoblo/Haushaltsbuch/internal/version.Date=${DATE}" \
    -o /out/haushaltsbuch ./cmd/haushaltsbuch
RUN mkdir -p /out/appdata

# ---- Runtime stage ----
FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY --from=builder /out/haushaltsbuch /app/haushaltsbuch
COPY --from=builder --chown=65532:65532 /out/appdata /app/appdata
ENV HB_ADDR=:8080
ENV HB_DB_PATH=/app/appdata/haushaltsbuch.db
EXPOSE 8080
VOLUME ["/app/appdata"]
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/app/haushaltsbuch", "-healthcheck"]
ENTRYPOINT ["/app/haushaltsbuch"]

# syntax=docker/dockerfile:1
ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build
WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -buildvcs=false \
      -ldflags="-s -w \
        -X github.com/daknoblo/Haushaltsbuch/internal/version.Version=${VERSION} \
        -X github.com/daknoblo/Haushaltsbuch/internal/version.Commit=${COMMIT} \
        -X github.com/daknoblo/Haushaltsbuch/internal/version.Date=${DATE}" \
      -o /out/haushaltsbuch ./cmd/haushaltsbuch
# Docker seeds a fresh named volume from the image, so /appdata has to exist
# with nonroot ownership. The distroless runtime has no shell to create it.
RUN mkdir -p /out/appdata

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/haushaltsbuch /app/haushaltsbuch
COPY --from=build --chown=65532:65532 /out/appdata /appdata
ENV TZ=Europe/Berlin
VOLUME ["/appdata"]
EXPOSE 8080
USER nonroot:nonroot
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/app/haushaltsbuch", "-healthcheck"]
ENTRYPOINT ["/app/haushaltsbuch"]

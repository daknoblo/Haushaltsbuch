# syntax=docker/dockerfile:1

# Both base images are pinned to the digest they had when this line was written.
# A tag can be moved to different content under the same name, so a tag alone
# means the build is not reproducible and cannot be audited after the fact. The
# readable tag stays in front of the digest to say what the digest is; Docker
# resolves the digest and ignores the tag. Dependabot keeps both in step.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine@sha256:28d89ee9cc0ff9fec75c82ca201e6bf7fdf9a679d4b7b24dfa04f2bb766bb468 AS build
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

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
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

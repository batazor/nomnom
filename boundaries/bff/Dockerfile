# syntax=docker/dockerfile:1.7
#
# BFF image (ADR 10): Cosmo Router + Go router plugin + execution config.
#   router  — pinned Cosmo Router release, verified by SHA-256 (mismatch fails the build)
#   plugin  — Go router plugin, plugins/bff/bin/<os>_<arch> as the Router expects it
#   config  — config.json composed by wgc from graph.yaml (package-lock.json, offline)
#   runtime — distroless static, nonroot: router, plugin, config.json, config.production.yaml

# Cosmo Router release https://github.com/wundergraph/cosmo/releases/tag/router%400.363.0
# (latest stable router@ release on 2026-10-25). SHA-256 of the release archives
# router-router@<version>-linux-<arch>.tar.gz: computed from the downloads and equal to the
# asset digests GitHub shows for the release; the published .md5 files match as well.
# Update the version and both checksums together.
ARG ROUTER_VERSION=0.363.0
ARG ROUTER_SHA256_AMD64=5ce7563465c4cfc926ef6ed070bd2995af62e95957e6862d940fdf77d6650329
ARG ROUTER_SHA256_ARM64=9a0a82a4108d4c5b30c84c270c41969c2aaedccc27adbd104352695a1e43915d

FROM --platform=$BUILDPLATFORM alpine:3.24 AS router

ARG ROUTER_VERSION
ARG ROUTER_SHA256_AMD64
ARG ROUTER_SHA256_ARM64
ARG TARGETOS
ARG TARGETARCH

RUN set -eu; \
    test "${TARGETOS}" = linux || { echo "unsupported TARGETOS ${TARGETOS}" >&2; exit 1; }; \
    case "${TARGETARCH}" in \
      amd64) sum="${ROUTER_SHA256_AMD64}" ;; \
      arm64) sum="${ROUTER_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    archive="router-router@${ROUTER_VERSION}-linux-${TARGETARCH}.tar.gz"; \
    wget -q -O /tmp/router.tar.gz \
      "https://github.com/wundergraph/cosmo/releases/download/router%40${ROUTER_VERSION}/${archive}"; \
    echo "${sum}  /tmp/router.tar.gz" | sha256sum -c -; \
    mkdir -p /out; \
    tar -xzf /tmp/router.tar.gz -C /out router; \
    rm /tmp/router.tar.gz

FROM --platform=$BUILDPLATFORM golang:1.27.3-trixie AS plugin

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src/plugins/bff

COPY plugins/bff/go.mod plugins/bff/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY plugins/bff/ ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -o "/out/plugins/bff/bin/${TARGETOS}_${TARGETARCH}" ./src

FROM --platform=$BUILDPLATFORM node:24.22.1-trixie-slim AS config

WORKDIR /src

COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund

COPY graph.yaml ./
COPY events/schema.graphql events/
COPY plugins/bff/src/schema.graphql plugins/bff/src/
COPY plugins/bff/generated/service.proto plugins/bff/generated/mapping.json plugins/bff/generated/
RUN mkdir -p /out && npx wgc router compose --input graph.yaml --out /out/config.json

FROM gcr.io/distroless/static-debian13:nonroot AS runtime

WORKDIR /app

COPY --from=router /out/router /app/router
COPY --from=plugin /out/plugins/ /app/plugins/
COPY --from=config /out/config.json /app/config.json
COPY config.production.yaml /app/config.yaml

# Tracing is opt-in per environment (config.production.yaml, telemetry.tracing).
ENV TRACING_ENABLED=false
EXPOSE 3010
USER nonroot:nonroot
ENTRYPOINT ["/app/router"]

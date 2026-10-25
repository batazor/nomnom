# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -o /out/ ./cmd/orders ./cmd/orders-worker ./cmd/orders-consumer ./cmd/orders-forwarder ./cmd/orders-migrate ./cmd/orders-reconciler

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build --chown=nonroot:nonroot /out/orders /orders
# Temporal worker того же контекста; запускается отдельным workload с command: ["/orders-worker"].
COPY --from=build --chown=nonroot:nonroot /out/orders-worker /orders-worker
# Outbox forwarder (ADR 13) и миграции PostgreSQL (init container).
COPY --from=build --chown=nonroot:nonroot /out/orders-forwarder /orders-forwarder
COPY --from=build --chown=nonroot:nonroot /out/orders-migrate /orders-migrate
# Reconciliation order#customer SpiceDB ↔ PostgreSQL (docs/security.md).
COPY --from=build --chown=nonroot:nonroot /out/orders-reconciler /orders-reconciler
# Kafka consumer того же контекста (ADR 28); command: ["/orders-consumer"].
COPY --from=build --chown=nonroot:nonroot /out/orders-consumer /orders-consumer

EXPOSE 8081 9081
USER nonroot:nonroot
ENTRYPOINT ["/orders"]

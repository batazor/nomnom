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
    go build -trimpath -o /out/ ./cmd/orders ./cmd/orders-worker ./cmd/orders-consumer ./cmd/orders-admin

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build --chown=nonroot:nonroot /out/orders /orders
# Temporal worker того же контекста; запускается отдельным workload с command: ["/orders-worker"].
COPY --from=build --chown=nonroot:nonroot /out/orders-worker /orders-worker
# Миграции встроены в процессы и применяются при старте; Outbox публикует Debezium (ADR 37).
# Kafka consumer того же контекста (ADR 28); command: ["/orders-consumer"].
COPY --from=build --chown=nonroot:nonroot /out/orders-consumer /orders-consumer
# Операторские действия: DLQ и перестроение проекций (kubectl exec deploy/orders-consumer -- /orders-admin …).
COPY --from=build --chown=nonroot:nonroot /out/orders-admin /orders-admin

EXPOSE 8081 9081
USER nonroot:nonroot
ENTRYPOINT ["/orders"]

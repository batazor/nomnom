# syntax=docker/dockerfile:1.7
#
# schema-registration (FR-712): контекст — корень репозитория ради .proto модулей контекстов,
# лишнее отсекает Dockerfile.dockerignore. Файлы компилирует protocompile (компилятор buf) при
# запуске, в Registry уходит их исходный текст.
FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY ops/schema-registration/go.mod ops/schema-registration/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY ops/schema-registration/*.go ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -o /out/schema-registration .

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build /out/schema-registration /schema-registration
COPY ops/schema-registration/event-files.yaml /schemas/event-files.yaml
COPY boundaries/orders/infrastructure/grpc/nomnom /schemas/boundaries/orders/infrastructure/grpc/nomnom
COPY boundaries/restaurants/infrastructure/grpc/nomnom /schemas/boundaries/restaurants/infrastructure/grpc/nomnom
COPY boundaries/delivery/infrastructure/grpc/nomnom /schemas/boundaries/delivery/infrastructure/grpc/nomnom
COPY boundaries/payments/infrastructure/grpc/nomnom /schemas/boundaries/payments/infrastructure/grpc/nomnom

USER nonroot:nonroot
ENTRYPOINT ["/schema-registration", "--root", "/schemas", "--config", "/schemas/event-files.yaml"]

# syntax=docker/dockerfile:1.7
# Образ проверок kind E2E (ops/kind/e2e): grpcurl и descriptor sets всех gRPC-контрактов
# nomnom, чтобы Chainsaw вызывал бизнес-API настоящим gRPC-клиентом изнутри неймспейса
# приложения (под его NetworkPolicy). Тот же образ у всех fixtures проверок
# (e2e/chart/templates/probes.yaml): grpcurl, nc и nslookup (busybox), curl для GraphQL BFF.
# Контекст сборки — корень репозитория; лишнее отсекает Dockerfile.dockerignore.

FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.24@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS grpcurl
ARG TARGETOS
ARG TARGETARCH
# Образа grpcurl v1.9.4 нет на Docker Hub, поэтому он собирается из закреплённого релиза.
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
      go install github.com/fullstorydev/grpcurl/cmd/grpcurl@v1.9.4 && \
    find /go/bin -name grpcurl -type f -exec cp {} /grpcurl \;

FROM --platform=$BUILDPLATFORM bufbuild/buf:1.75.0@sha256:adf82c7170be6629ec81dc907921ac1699407cd11c4f7fd7521b13c74152f849 AS protos
WORKDIR /src
COPY buf.yaml buf.lock ./
COPY boundaries/orders/infrastructure/grpc/nomnom boundaries/orders/infrastructure/grpc/nomnom
COPY boundaries/restaurants/infrastructure/grpc/nomnom boundaries/restaurants/infrastructure/grpc/nomnom
COPY boundaries/delivery/infrastructure/grpc/nomnom boundaries/delivery/infrastructure/grpc/nomnom
COPY boundaries/payments/infrastructure/grpc/nomnom boundaries/payments/infrastructure/grpc/nomnom
COPY boundaries/controlplane/infrastructure/grpc/nomnom boundaries/controlplane/infrastructure/grpc/nomnom
COPY ops/kind/e2e/probe/proto /fixtures
# Buf image совместим с FileDescriptorSet и уже содержит импорты (well-known types).
# google/rpc из googleapis (закреплённый commit BSR) — чтобы grpcurl декодировал детали
# ошибок (google.rpc.ErrorInfo) и проверки сверяли reason.
RUN mkdir /out && buf build --exclude-source-info -o /out/nomnom.binpb && \
    buf build /fixtures --exclude-source-info -o /out/fixtures.binpb && \
    buf build buf.build/googleapis/googleapis:8ac61b9e502343289c8c42b17aa5b245 --path google/rpc \
      --exclude-source-info -o /out/googleapis.binpb

FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a AS probe
# curl — HTTP-клиент проверок GraphQL BFF; nc и nslookup даёт busybox базового образа.
RUN apk add --no-cache curl
COPY --from=grpcurl /grpcurl /usr/local/bin/grpcurl
COPY --from=protos /out /protos
USER 100:100
ENTRYPOINT ["sleep", "infinity"]

# Seed демо-данных (chart ops/kind/demo): тот же grpcurl и descriptor sets, python3 и openssl
# для подписи user assertions (scripts/mint-jwt.py). Skaffold собирает его target demo-seed.
FROM probe AS demo-seed
USER root
RUN apk add --no-cache python3 openssl
COPY ops/kind/scripts/mint-jwt.py /seed/mint_jwt.py
COPY ops/kind/demo/seed/seed.py /seed/seed.py
USER 100:100
ENTRYPOINT []

# Образ проверок — последняя стадия, чтобы сборка без target давала его.
FROM probe

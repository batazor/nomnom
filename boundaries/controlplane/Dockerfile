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
    go build -trimpath -o /out/ ./cmd/controlplane

FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=build --chown=nonroot:nonroot /out/controlplane /controlplane

EXPOSE 9086
USER nonroot:nonroot
ENTRYPOINT ["/controlplane"]

# syntax=docker/dockerfile:1.7

FROM rust:1.99.1-bookworm AS build

# librdkafka (rdkafka-sys) собирается из исходников: C toolchain, cmake, zlib.
RUN apt-get update && \
    apt-get install --no-install-recommends -y build-essential cmake zlib1g-dev && \
    rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY Cargo.toml Cargo.lock ./
COPY lib.rs ./lib.rs
COPY cmd ./cmd
COPY di ./di
COPY infrastructure ./infrastructure
COPY pkg ./pkg
COPY slices ./slices
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    --mount=type=cache,target=/src/target \
    CARGO_PROFILE_RELEASE_DEBUG=1 cargo build --release --locked && \
    cp target/release/delivery /out-delivery && \
    cp target/release/delivery-relay /out-delivery-relay && \
    cp target/release/delivery-migrate /out-delivery-migrate

FROM debian:12.12-slim AS runtime

RUN apt-get update && \
    apt-get install --no-install-recommends -y ca-certificates tini zlib1g && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --system --gid 10001 app && \
    useradd --system --uid 10001 --gid app --no-create-home --home-dir /nonexistent app

COPY --from=build --chown=app:app /out-delivery /usr/local/bin/delivery
COPY --from=build --chown=app:app /out-delivery-relay /usr/local/bin/delivery-relay
COPY --from=build --chown=app:app /out-delivery-migrate /usr/local/bin/delivery-migrate

EXPOSE 8083 9083
USER app:app
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/delivery"]

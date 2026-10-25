# syntax=docker/dockerfile:1.7

FROM rust:1.91.0-bookworm AS build

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
    cp target/release/delivery /out-delivery

FROM debian:12.12-slim AS runtime

RUN apt-get update && \
    apt-get install --no-install-recommends -y ca-certificates tini && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --system --gid 10001 app && \
    useradd --system --uid 10001 --gid app --no-create-home --home-dir /nonexistent app

COPY --from=build --chown=app:app /out-delivery /usr/local/bin/delivery

EXPOSE 8083 9083
USER app:app
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/delivery"]

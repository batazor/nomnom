# syntax=docker/dockerfile:1.7

FROM rust:1.99.1-bookworm AS build

WORKDIR /src

COPY Cargo.toml Cargo.lock ./
COPY lib.rs ./lib.rs
COPY cmd ./cmd
COPY di ./di
COPY infrastructure ./infrastructure
COPY pkg ./pkg
COPY slices ./slices
# Offline query cache of the compile-time checked sqlx macros (`make sqlx-prepare`):
# the build checks queries against it and never connects to a database.
COPY .sqlx ./.sqlx
ENV SQLX_OFFLINE=true
# target — общий cache mount всех сборок на хосте, в том числе других деревьев исходников.
# Cargo сравнивает mtime исходников с fingerprint, а COPY сохраняет mtime контекста: более
# старый файл другого содержимого выглядел бы собранным, и в образ попал бы чужой бинарь.
# touch заставляет пересобрать сам crate (зависимости остаются в кеше), locked — не даёт
# параллельной сборке писать в тот же target.
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/usr/local/cargo/git \
    --mount=type=cache,target=/src/target,sharing=locked \
    find lib.rs cmd di infrastructure pkg slices -name '*.rs' -exec touch {} + && \
    CARGO_PROFILE_RELEASE_DEBUG=1 cargo build --release --locked && \
    cp target/release/delivery /out-delivery && \
    cp target/release/delivery-worker /out-delivery-worker

FROM debian:12.12-slim AS runtime

RUN apt-get update && \
    apt-get install --no-install-recommends -y ca-certificates tini && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --system --gid 10001 app && \
    useradd --system --uid 10001 --gid app --no-create-home --home-dir /nonexistent app

COPY --from=build --chown=app:app /out-delivery /usr/local/bin/delivery
# Temporal worker Assignment Workflow (FR-708): тот же образ, command /usr/local/bin/delivery-worker.
COPY --from=build --chown=app:app /out-delivery-worker /usr/local/bin/delivery-worker

EXPOSE 8083 9083
USER app:app
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["/usr/local/bin/delivery"]

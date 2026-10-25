# syntax=docker/dockerfile:1.7

FROM ghcr.io/astral-sh/uv:0.12.7 AS uv

FROM python:3.15.0-slim-bookworm AS build

ENV UV_COMPILE_BYTECODE=1 \
    UV_LINK_MODE=copy
WORKDIR /app

COPY --from=uv /uv /usr/local/bin/uv
COPY pyproject.toml uv.lock ./
RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --frozen --no-dev --no-install-project

FROM build AS build-dev

RUN --mount=type=cache,target=/root/.cache/uv \
    uv sync --frozen --no-dev --group watch --no-install-project

FROM python:3.15.0-slim-bookworm AS base

ENV PATH="/app/.venv/bin:$PATH" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1
WORKDIR /app

RUN apt-get update && \
    apt-get install --no-install-recommends -y tini && \
    rm -rf /var/lib/apt/lists/* && \
    groupadd --system --gid 10001 app && \
    useradd --system --uid 10001 --gid app --no-create-home --home-dir /nonexistent app

EXPOSE 8084 9084
ENTRYPOINT ["/usr/bin/tini", "--"]

# Только для make kind-dev (Skaffold profile dev): watchfiles перезапускает процесс после
# file sync исходников без пересборки образа.
FROM base AS dev

RUN chown app:app /app
COPY --from=build-dev --chown=app:app /app/.venv /app/.venv
COPY --chown=app:app entrypoints ./entrypoints
COPY --chown=app:app infrastructure ./infrastructure
COPY --chown=app:app pkg ./pkg
COPY --chown=app:app slices ./slices

USER app:app
CMD ["watchfiles", "--filter", "python", "python -m entrypoints.payments.main", \
     "entrypoints", "infrastructure", "pkg", "slices"]

FROM base AS runtime

COPY --from=build --chown=app:app /app/.venv /app/.venv
COPY --chown=app:app entrypoints ./entrypoints
COPY --chown=app:app infrastructure ./infrastructure
COPY --chown=app:app pkg ./pkg
COPY --chown=app:app slices ./slices

USER app:app
CMD ["python", "-m", "entrypoints.payments.main"]

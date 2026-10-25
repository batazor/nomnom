# syntax=docker/dockerfile:1.7

FROM node:24.11.0-bookworm-slim AS deps

WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

FROM deps AS build

COPY tsconfig.json ./
COPY cmd ./cmd
COPY di ./di
COPY infrastructure ./infrastructure
COPY pkg ./pkg
COPY slices ./slices
RUN npm run build

# Только для make kind-dev (Skaffold profile dev): исходники .ts запускаются напрямую,
# node --watch перезапускает процесс после file sync без пересборки образа.
FROM deps AS dev

RUN apt-get update && \
    apt-get install --no-install-recommends -y tini && \
    rm -rf /var/lib/apt/lists/* && \
    chown node:node /app

COPY --chown=node:node cmd ./cmd
COPY --chown=node:node di ./di
COPY --chown=node:node infrastructure ./infrastructure
COPY --chown=node:node pkg ./pkg
COPY --chown=node:node slices ./slices

EXPOSE 8082 9082
USER node
ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["node", "--watch", "cmd/restaurants/main.ts"]

FROM node:24.11.0-bookworm-slim AS production-deps

WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --omit=dev

FROM node:24.11.0-bookworm-slim AS runtime

ENV NODE_ENV=production
WORKDIR /app

RUN apt-get update && \
    apt-get install --no-install-recommends -y tini && \
    rm -rf /var/lib/apt/lists/*

COPY --from=production-deps --chown=node:node /app/node_modules ./node_modules
COPY --chown=node:node package.json package-lock.json ./
COPY --from=build --chown=node:node /app/dist ./dist

EXPOSE 8082 9082
USER node
ENTRYPOINT ["/usr/bin/tini", "--"]
# The same image runs restaurants-consumer (ADR 28): its Deployment overrides
# the command with `node dist/cmd/restaurants-consumer/main.js` (management
# port 9082 only),
# restaurants-migrate with `node dist/cmd/restaurants-migrate/main.js`.
CMD ["node", "dist/cmd/restaurants/main.js"]

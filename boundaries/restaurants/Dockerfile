# syntax=docker/dockerfile:1.7

FROM node:24.11.0-bookworm-slim AS deps

WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

FROM deps AS build

COPY tsconfig.json ./
COPY cmd ./cmd
COPY infrastructure ./infrastructure
COPY pkg ./pkg
COPY slices ./slices
RUN npm run build

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
CMD ["node", "dist/cmd/restaurants/main.js"]

# Customer UI image (local ADR 2 of UI): `vite build` served by nginx as static files.
#   build   — node, npm ci, tsc and vite build; generated GraphQL documents are committed
#   runtime — nginx-unprivileged (nonroot, port 8080): dist, nginx templates
# The environment is not built in: the chart mounts /config.js (window.__NOMNOM_CONFIG__)
# and sets UI__CONTENT_SECURITY_POLICY, so one image serves every environment.
# Build context: boundaries/ (Vite reads the client schema of BFF from ../bff, alias
# @public-schema); ui/Dockerfile.dockerignore keeps only the UI and that schema.
FROM node:24.11.0-bookworm-slim AS build
WORKDIR /src/ui
COPY ui/package.json ui/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund
COPY bff/client-schema.graphql /src/bff/client-schema.graphql
COPY ui/ ./
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.30.6-alpine-slim AS runtime
COPY ui/ops/nginx/default.conf.template /etc/nginx/templates/default.conf.template
COPY ui/ops/nginx/security-headers.inc.template /etc/nginx/templates/security-headers.inc.template
COPY ui/ops/nginx/05-require-environment.sh /docker-entrypoint.d/05-require-environment.sh
COPY --from=build /src/ui/dist /usr/share/nginx/html
EXPOSE 8080

# Customer UI operations

Операционные файлы deployable customer UI ([ADR 12](../../../docs/adr/0012-helm-charts-per-deployable-application.md)). Решение — [локальный ADR 2](../docs/adr/0002-static-origin-on-nginx-with-runtime-config.md): статика `vite build` за nginx, окружение в `/config.js`, один образ для всех окружений.

- [`../Dockerfile`](../Dockerfile) — образ: `vite build` и `nginxinc/nginx-unprivileged`; контекст сборки — `boundaries/` (клиентская схема BFF), лишнее отсекает [`../Dockerfile.dockerignore`](../Dockerfile.dockerignore).
- [`nginx/`](nginx/) — origin: SPA-фолбек на `index.html`, кеш (`index.html` и `/config.js` без кеша, `/assets/*` — immutable), Content-Security-Policy, `Connection-Allowlist` и security headers, `/healthz`. Без `UI__CONTENT_SECURITY_POLICY` и `UI__CONNECTION_ALLOWLIST` контейнер не стартует. `vite preview` отдаёт те же заголовки и `/config.js` из Vite env.
- [`helm/`](helm/) — chart: Deployment, Service, ConfigMap с `/config.js` из `config.*` (обязательные поля не заданы — chart не рендерится), CSP из тех же адресов, ReferenceGrant для HTTPRoute публичного gateway.

Перед UI стоит Cloudflare (TLS, CDN, WAF); origin работает за ним по требованиям [AGENTS.md](../AGENTS.md). Production-hosting (домен, edge-правила) ещё не описан; chart сейчас разворачивается в kind (`ops/kind/values/ui.yaml`, `https://app.nomnom.localhost:18085`).

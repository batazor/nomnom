# 2. Origin customer UI: статика на nginx, окружение в /config.js

Date: 2026-10-28

## Status

Accepted

## Context

Customer UI — SPA (`vite build`): после сборки это набор статических файлов, серверного рендеринга нет. Перед ним всегда стоит Cloudflare (TLS, CDN, WAF; `boundaries/ui/AGENTS.md`), поэтому origin отвечает за security headers и за то, что CDN может кешировать.

Окружение приложения (адрес публичного API, OIDC issuer, Faro collector, locale) задаётся переменными `VITE_*`, которые Vite подставляет в бандл при сборке. Образ, собранный под одно окружение, не годится для другого, а сборка под каждое окружение нарушает принцип «один артефакт — много окружений» (ADR 17).

До этого решения UI не разворачивался ни в одном окружении: в kind работал только Vite dev server, и путь production-сборки (persisted operations, CSP, кеш) нигде не проверялся.

## Decision

### Образ и origin

Образ `boundaries/ui/Dockerfile`: стадия `build` (node, `npm ci`, `vite build`) и `runtime` на `nginxinc/nginx-unprivileged` (закреплённый stable-тег, nonroot, порт 8080). Контекст сборки — `boundaries/`: Vite читает клиентскую схему BFF из `../bff/client-schema.graphql`; `ui/Dockerfile.dockerignore` пропускает только исходники UI и эту схему.

nginx (`ops/nginx/default.conf.template`):

- любой путь, кроме существующего файла, отдаёт `index.html` (SPA);
- `index.html` — `Cache-Control: no-cache`, хешированные `/assets/*` — `public, max-age=31536000, immutable`, `/config.js` — `no-store`; так Cloudflare кеширует только неизменяемое, а новый релиз виден сразу;
- каждый ответ несёт `Content-Security-Policy`, `Connection-Allowlist`, `X-Content-Type-Options: nosniff` и `Referrer-Policy: strict-origin-when-cross-origin`;
- `/healthz` — probes Pod.

### Окружение во время выполнения

Окружение не встраивается в образ. Chart (`ops/helm`) монтирует `/config.js` из ConfigMap: `window.__NOMNOM_CONFIG__ = Object.freeze({...})` с теми же ключами `VITE_*`. `index.html` подключает его обычным `<script>` до приложения; собранное приложение читает конфигурацию только оттуда и падает при старте, если `/config.js` её не задал (заглушка в образе ничего не задаёт). Dev server и тесты читают `import.meta.env`, как раньше. Правила валидации общие (`src/shared/config/runtime-config.ts`).

Chart проверяет обязательные поля (`config.publicApiUrl`, `locale`, `deploymentEnvironment`, `oidc.*`) и не рендерится без них; моки в развёрнутом UI выключены.

### Content-Security-Policy

CSP и `Connection-Allowlist` строит chart из тех же адресов, что `/config.js`, и передаёт контейнеру в `UI__CONTENT_SECURITY_POLICY` и `UI__CONNECTION_ALLOWLIST`; без них контейнер не стартует. Политика: `default-src 'self'`, `script-src 'self'` (inline-скриптов нет, `config.js` — файл), `style-src 'self'` плюс хеш единственного inline `<style>` react-aria (motion пишет стили через CSSOM, CSP его не ограничивает), `connect-src 'self'` плюс origin публичного API, OIDC issuer и Faro collector, `object-src 'none'`, `base-uri 'self'`, `form-action 'self'`, `frame-ancestors 'none'`. Прежний meta-тег CSP, который сборка вшивала из build-time env, убран: политику задаёт origin. `vite preview` отдаёт ту же политику и `/config.js` из Vite env (`vite.config.ts`), поэтому локальная сборка работает как развёрнутая.

### kind

Release `ui` в неймспейсе `nomnom-ui` (Skaffold, `ops/kind/values/ui.yaml`); listener `app` публичного gateway (`app.nomnom.localhost`, тот же сертификат локального CA) и HTTPRoute `ui` ведут в Service UI. Браузер открывает `https://app.nomnom.localhost:18085`. NetworkPolicy пускают в UI только public gateway. Dex разрешает redirect URI и origin этого адреса. Вход — через Dex на listener `auth` того же gateway (ADR 35), без port-forward. Profile `grafana-cloud` включает Faro (`values/ui-grafana-cloud.yaml`). Chainsaw-тест `ops/kind/e2e/ui` проверяет заголовки, SPA-фолбек, кеш и `/config.js`.

## Consequences

Один образ UI работает в любом окружении; окружение меняется ConfigMap без пересборки. Путь production-сборки — persisted operations BFF, CSP, кеш — проверяется в kind.

Цена: дополнительный запрос `/config.js` до старта приложения и его синхронная загрузка; хеш inline-стиля react-aria задан в двух местах (`vite.config.ts` и chart); root filesystem контейнера не read-only, потому что образ nginx пишет конфигурацию из шаблонов при старте.

Production-hosting за Cloudflare (реальный домен, TLS, правила кеша на edge) этим решением не описывается: chart рассчитан на kind и станет его основой.

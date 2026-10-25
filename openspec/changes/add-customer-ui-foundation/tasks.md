# Tasks

## 1. Tooling и application boundary

- [x] 1.1 Создать независимую application boundary `boundaries/ui/` с Vite + React + TypeScript scaffold, `package.json`, lockfile, strict TypeScript, принятым в TypeScript-приложениях репозитория Biome и scripts `dev`, `build`, `typecheck`, `lint`, `format`, `test`, `check`; проверить установку через `npm ci` и компиляцию через `npm run typecheck`.
- [x] 1.2 Добавить typed runtime config с единственным business endpoint `VITE_PUBLIC_GRAPHQL_URL`, development example и fail-fast validation; покрыть допустимое и отсутствующее значение unit-тестом и проверить целевым запуском Vitest.

## 2. Tokens и Astra foundation

- [x] 2.1 Подключить Tailwind CSS 4, `@astra-rn/react`, `@astra-rn/styles` и прямую React Aria dependency с exact/pinned версиями; проверить production CSS сборкой `npm run build` и наличие MIT metadata Astra в lockfile/package metadata.
- [x] 2.2 Создать `shared/tokens/` с семантическими color, typography, spacing, radius, shadow и motion variables для light/dark и `prefers-reduced-motion`; добавить fixture/test, проверяющий обязательные tokens и отсутствие hardcoded palette в локальном UI component.
- [x] 2.3 Создать `shared/ui/` façade с первым доступным Button и layout primitive, исключить прямые Astra imports из feature/app code; проверить keyboard/focus semantics component-тестом через Testing Library и axe-compatible assertion.

## 3. Routing shell, i18n и placeholder

- [x] 3.1 Реализовать `app/` bootstrap, providers, router и mobile-first layout с landmark semantics и одним route; проверить navigation/render component-тестом на mobile viewport.
- [x] 3.2 Реализовать минимальный typed i18n-layer, `ru` messages и `Intl` formatter factories с locale из config; проверить translation lookup, fallback и locale-aware number/date formatting unit-тестами.
- [x] 3.3 Добавить локализованный placeholder-экран с одним primary CTA и без fake product data/backend call; проверить rendered text, единственный primary action и отсутствие горизонтального overflow shell-тестом.

## 4. GraphQL foundation

- [x] 4.1 Добавить provisional urql client provider, использующий только validated public GraphQL URL и seam для correlation/trace propagation без отправки variables в logs; проверить endpoint wiring и redaction unit-тестом.
- [x] 4.2 Настроить GraphQL Code Generator `client` preset: public BFF schema default/override, `src/features/**/*.graphql`, output в `shared/graphql/generated/`, `ignoreNoDocuments`; выполнить generation против текущей `boundaries/bff/plugins/bff/src/schema.graphql` и проверить повторяемость без diff.
- [x] 4.3 Добавить dependency-boundary check, запрещающий imports внутренних `boundaries/`, BFF plugin/generated gRPC, Kafka/Temporal/DB clients и прямые Astra imports вне `shared/ui/`; проверить, что lint/check проходит на scaffold и тестовая запрещённая fixture обнаруживается.

## 5. Архитектурная документация и ADR

- [x] 5.1 Создать `boundaries/ui/README.md` с назначением, запуском, stack/structure, Cosmo GraphQL boundary, текущей package-based моделью Astra, MIT/free scope, update procedure и React Aria exit strategy; проверить все локальные ссылки и команды из README.
- [x] 5.2 Создать `docs/ui.md` с design principles, feature organization, tokens, a11y, responsive, i18n, state/GraphQL, auth/authorization, errors/loading, order-status mapping, polling, idempotency, observability, testing и open decisions; сверить термины с glossary/BFF/SpiceDB/observability ADR.
- [x] 5.3 Создать `boundaries/ui/ops/README.md`, объясняющий ownership operational files и отсутствие manifests до hosting/SSR решения; проверить соответствие общесистемному ADR 12 и отсутствие пустых Docker/Helm artifacts.
- [x] 5.4 Через корневой ADR CLI создать Accepted ADR `Astra UI as the Frontend Component Foundation`, связать его с BFF Cosmo, observability и SpiceDB ADR, исключить SPA/client/auth решения и зафиксировать package-based deviation; проверить ADR numbering/status и ссылки.
- [x] 5.5 Добавить в корневой `README.md` ссылки на customer UI и `docs/ui.md`; проверить Markdown links локальным link check или разрешением всех изменённых относительных путей.

## 6. Интеграционная проверка

- [x] 6.1 Выполнить `npm run check` и clean `npm ci && npm run check && npm run build` в `boundaries/ui/`, устранить warnings/errors и зафиксировать успешные команды в итогах изменения.
- [x] 6.2 Выполнить `openspec validate add-customer-ui-foundation --strict`, проверить `git diff --check` и вручную подтвердить отсутствие продуктовых экранов, бизнес-логики, Pro/React Native Astra и внутренних backend endpoints.

## Workflow follow-up

- После review и проверки реализации архивировать change через OpenSpec archive workflow.
- При archive синхронизировать `customer-web-application` в main specs и повторить strict validation.

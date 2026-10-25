# 23. Astra UI as the Frontend Component Foundation

Date: 2026-10-24

## Status

Accepted

## Context

nomnom нужен единый frontend foundation для customer web-приложения до реализации restaurant discovery, меню, корзины и заказов. Компоненты должны сохранять keyboard, focus, semantic и screen-reader поведение, поддерживать mobile-first layout, light/dark theme и единый semantic token layer.

[Локальный ADR 3 BFF](../../boundaries/bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md) уже определил WunderGraph Cosmo Router и публичный GraphQL API единственной клиентской точкой входа. [ADR 9](0009-использовать-opentelemetry-и-grafana-observability-stack.md) задаёт W3C trace context и правила telemetry, а [ADR 18](0018-spicedb-for-authorization.md) оставляет authoritative authorization backend-владельцам use cases.

Astra web UI построен поверх React, React Aria Components и Tailwind CSS. Актуальный официальный способ распространения web-компонентов — npm packages `@astra-rn/react` и `@astra-rn/styles` с MIT metadata. Это расходится с первоначальным предположением, что исходники Astra копируются в репозиторий. React Native/Expo animations и Astra Pro к customer web UI не относятся.

## Decision

Разместить customer web-приложение как самостоятельную application boundary в `boundaries/ui/`. Использовать React и TypeScript; Tailwind CSS и React Aria Components являются foundation, а Astra — единственным styled component layer поверх них.

Подключать Astra через exact-pinned официальные web packages. Upstream package не называется vendored или собственным кодом. Владение проектом обеспечивается локальной façade в `src/shared/ui/`: только она импортирует Astra, задаёт nomnom defaults и экспортирует primitives в feature/app code. При обновлении packages вручную проверяются license metadata, API, visual и accessibility regressions. Exit strategy — заменить реализацию façade на React Aria Components без изменения feature imports. Второй component framework не добавляется.

Организовать frontend по feature boundaries. GraphQL operations принадлежат feature и лежат рядом с её UI и тестами; `shared/` содержит только реально переиспользуемые tokens, UI façade, GraphQL transport/generated artifacts, config и i18n. Глобальные технические каталоги `components/`, `hooks/`, `services/` и `utils/` не формируют каркас приложения.

Все визуальные значения выражаются semantic color, typography, spacing, radius, shadow и motion tokens. Foundation mobile-first, поддерживает light/dark и `prefers-reduced-motion`, сохраняет React Aria accessibility semantics и проверяется component/a11y tests.

Публичный GraphQL API Cosmo — единственная business API boundary браузера. TypeScript documents/types генерируются из публичной schema через GraphQL Code Generator; frontend не импортирует и не вызывает bounded-context API, gRPC, Kafka, Temporal, database clients или internal gateway. UI может использовать доступность actions из API только для UX; authorization продолжает выполняться backend-владельцем.

В это решение не входят и остаются открытыми:

- SPA, SSR или SSG production architecture;
- окончательный GraphQL client и cache policy;
- authentication/session mechanism и token storage;
- cart persistence, subscriptions/SSE и browser telemetry SDK;
- hosting, CSP и deployment model.

## Consequences

Customer flows получают одну доступную визуальную основу, semantic tokens и проверяемую публичную API boundary. Feature code не зависит напрямую от Astra, поэтому package API можно адаптировать локально или заменить на React Aria Components.

Package-based distribution упрощает воспроизводимую установку, но означает, что upstream source не принадлежит репозиторию. Exact pins требуют ручных dependency updates и regression checks. Если физическое vendoring станет обязательным, потребуется отдельное решение о получении source, NOTICE/license tracking и update workflow.

Vite scaffold остаётся provisional и не означает принятия SPA как production architecture. До реализации SEO-чувствительного discovery нужно измерить indexability, metadata, Web Vitals и latency requirements.

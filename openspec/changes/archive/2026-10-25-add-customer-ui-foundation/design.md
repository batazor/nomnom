# Design

## Context

См. мотивацию в `proposal.md` и contract в `specs/customer-web-application/spec.md`.

В репозитории пока нет customer frontend. BFF уже принят как единая публичная GraphQL-точка входа на WunderGraph Cosmo: допустимый путь — `browser → edge → Cosmo Router → BFF plugin → internal gateway → bounded context`. Общесистемные ADR требуют OpenTelemetry/W3C trace context, backend-enforced authorization через SpiceDB и размещение deploy/runtime-файлов каждого приложения в его `ops/`.

Проверка актуальной документации Astra выявила расхождение с исходным предположением:

- web UI kit построен на React, React Aria Components и Tailwind CSS 4, поддерживает light/dark темы и доступность;
- web-компоненты сейчас устанавливаются пакетами `@astra-rn/react` и `@astra-rn/styles`, а не копируются CLI в проект;
- npm metadata обоих пакетов версии 1.0.1 указывает MIT;
- бесплатный web UI kit разрешён для личного и коммерческого использования;
- отдельные Astra animations относятся к React Native/Expo и могут быть Pro — они не входят в этот design.

Источники: [Astra components](https://astra-ui.com/components), [Button installation](https://astra-ui.com/components/button), [Astra FAQ](https://astra-ui.com/), [Astra Pro](https://astra-ui.com/pro).

## Goals / Non-Goals

**Goals:**

- получить воспроизводимый, запускаемый и тестируемый frontend foundation без продуктовой логики;
- сделать публичную GraphQL boundary и feature ownership очевидными из структуры и tooling;
- обеспечить семантические tokens, responsive shell, a11y и i18n с первого экрана;
- оставить явные extension points для auth, telemetry, product operations и deployment;
- сохранить возможность выйти на чистые React Aria Components без переписывания feature imports.

**Non-Goals:**

- реальные restaurant, menu, cart, checkout, active-order и history экраны;
- GraphQL operations поверх ещё не утверждённых product fields;
- cart persistence strategy, authentication flow, payment UI или backend authorization;
- SSR, browser OTel SDK, Storybook, production image, Helm chart или CI deployment;
- Astra Pro, React Native/Expo animations и второй component framework.

## Decisions

### 1. Отдельная application boundary `boundaries/ui/`

Customer UI располагается в общем каталоге application boundaries наравне с другими сервисами. Каталог `boundaries/` задаёт границу приложения; это размещение само по себе не объявляет UI backend-сервисом или владельцем доменной модели:

```text
boundaries/ui/
  src/
    app/                 # bootstrap, providers, router, layout
    features/
      browse-restaurants/
      view-restaurant/
      cart/
      place-order/
      view-order/
      order-history/
      cancel-order/
    shared/
      ui/                # Astra adapters и nomnom wrappers
      tokens/            # CSS semantic tokens
      graphql/           # client bootstrap и generated output
      auth/
      config/
      i18n/
  ops/
```

На этом шаге создаются только реально используемые `app/` и `shared/` части; будущие feature-каталоги описываются в документации, но пустые каталоги не коммитятся. Запрещены глобальные `components/`, `hooks/`, `services/`, `utils/`: feature-specific code colocated внутри feature; перенос в `shared/` происходит только после реального повторного использования.

Альтернатива — поместить UI в `boundaries/bff/`. Отклонена: browser application имеет независимые build/deploy lifecycle и не является BFF plugin.

### 2. Vite SPA как provisional baseline

Scaffold использует React + TypeScript + Vite и React Router с одним route. Это минимальная, легко заменяемая база и предложенный default, пока SEO/performance требования discovery не измерены.

SPA/SSR остаётся open decision, а не частью ADR. До продуктовой реализации нужно определить indexability, metadata, web vitals и latency targets для restaurant discovery. Если SEO/initial-render требования подтвердятся, выбирается SSR-capable framework отдельным решением; feature и shared boundaries должны пережить миграцию.

Альтернатива — сразу Next.js/другой SSR framework. Отклонена на foundation-шаге из-за отсутствия подтверждённых требований и лишней runtime/deployment поверхности.

### 3. Astra через официальные web packages, ownership через локальную façade

Tailwind CSS 4 совместим с текущим Astra и подключается через:

```css
@import "tailwindcss";
@import "@astra-rn/styles";
```

`shared/ui/` экспортирует только используемые компоненты через локальные adapters/wrappers. Feature-код не импортирует `@astra-rn/react` напрямую. Первый placeholder использует локальный `Button`, который сохраняет React Aria props/semantics и применяет nomnom defaults.

Это сознательное отступление от требования «Astra source vendored»: актуальный официальный web distribution не копирует исходник компонента, а ставит package. Upstream package не объявляется нашим кодом. Нашими являются façade, wrappers, tokens и модификации в `shared/ui/`; version update — ручной PR с visual/a11y regression checks. Exit strategy — заменить реализацию façade на React Aria Components, сохранив feature imports.

Если обязательным станет физический vendoring upstream source, это требует отдельной оценки механизма получения source, NOTICE/license tracking и обновлений; распаковка compiled npm package без documented workflow не принимается скрыто.

Второй component framework запрещён. Если Astra primitive не подходит, сначала меняется локальный wrapper, затем используется React Aria напрямую внутри `shared/ui/`.

### 4. Семантические design tokens в CSS

`shared/tokens/theme.css` определяет CSS variables по категориям:

- color: `background`, `surface`, `foreground`, `muted`, `primary`, `success`, `warning`, `danger`, `border`;
- typography;
- spacing;
- radius;
- shadow;
- motion.

Tailwind theme mapping обращается к этим variables. Палитра является implementation detail tokens и не используется напрямую в компонентах. Dark theme следует системному preference по умолчанию и допускает будущий user override через атрибут на root. Motion tokens обнуляют/сокращают non-essential animation при `prefers-reduced-motion`.

### 5. GraphQL boundary и code generation

Frontend runtime получает один `VITE_PUBLIC_GRAPHQL_URL`; config module валидирует его и не содержит внутренних адресов. Для development default допустим только публичный локальный Cosmo Router endpoint.

GraphQL Code Generator использует `client` preset. Source schema задаётся через `GRAPHQL_SCHEMA` (URL или локальный artifact публичной schema), operations ищутся в `src/features/**/*.graphql`, output — `src/shared/graphql/generated/`. Generated files не редактируются вручную. На foundation-шаге product operations не создаются.

Runtime client provisional default — `urql`: он меньше по поверхности, достаточно покрывает normalized document execution/cache/exchanges и не навязывает глобальную копию server state. Выбор документируется как open decision до реальных cache/pagination/offline requirements. Apollo остаётся альтернативой, если подтвердятся его devtools/cache policy benefits.

Server state принадлежит GraphQL cache. Локальный UI state хранит modal/selection/preferences; form state остаётся у формы. Глобальный store, дублирующий GraphQL entities, не вводится.

### 6. App shell, providers и placeholder

`main.tsx` создаёт root и подключает global CSS. `app/providers.tsx` собирает GraphQL, routing и будущие cross-cutting providers. `app/router.tsx` объявляет один route; `app/layout.tsx` предоставляет compact consumer navigation, `main` landmark и responsive content container.

Placeholder демонстрирует только foundation: локализованный текст, семантический Card/Surface и один primary CTA без backend вызова. Он не имитирует продуктовый restaurant discovery и не содержит fake domain data.

### 7. i18n и форматирование без преждевременного framework

Минимальный typed messages layer хранит locale в config и предоставляет translation lookup. Первый locale — `ru`; fallback explicit. `Intl.NumberFormat`, `Intl.DateTimeFormat` и `Intl.PluralRules` создаются централизованными formatter factories. Это сохраняет migration path к i18next/FormatJS, когда появятся pluralization, extraction и translation workflow requirements.

Hardcoded user-visible strings вне messages запрещены. Technical identifiers, test selectors и aria values без видимого текста не считаются пользовательскими строками.

### 8. Будущие product conventions документируются, но не реализуются

`docs/ui.md` фиксирует:

- features: browse restaurants, restaurant menu, cart, checkout/place order, active order, order history/details, cancellation;
- order presentation mapping `backend state → UI status → localized label/icon/progress`, где внутренние FSM/Temporal names не показываются;
- active order default — bounded polling только пока экран активен; subscriptions/SSE требуют отдельного решения;
- mutation states: loading/disabled/success/business error/transport error/retry;
- automatic mutation retry запрещён без idempotency contract;
- checkout attempt генерирует idempotency key один раз и reuse его при ручном retry; формат берётся из backend contract;
- cart local/server-backed strategy остаётся product open decision.

### 9. Authentication, authorization и errors

Authentication остаётся open decision: OIDC/session и httpOnly cookie/BFF session предпочтительнее browser-readable long-lived tokens; `localStorage` требует отдельного threat-model justification.

UI может скрывать actions по данным API только как UX. Это не security boundary: authoritative authorization остаётся у backend owner согласно ADR 18. Frontend не моделирует SpiceDB graph и не принимает permission decisions самостоятельно.

Validation error отображается у поля, business error — business language, infrastructure error — generic recovery UI. Внутренние gRPC/Kafka/Temporal details и stack traces пользователю не передаются. Loading использует layout-stable skeleton; mutation state локален и не блокирует всё приложение.

### 10. Observability hooks без browser SDK

GraphQL transport предусматривает propagation разрешённого `traceparent`, если BFF/edge contract это поддерживает, и чтение correlation/request id из безопасного response header. Error boundary предоставляет adapter seam для будущего error reporter. Web performance telemetry и browser OTel setup требуют отдельного решения.

Запрещается отправлять PII, payment details, auth secrets, full GraphQL variables и high-cardinality resource identifiers как metric labels. Development logging следует той же redaction policy.

### 11. Testing defaults

- Vitest + Testing Library + `@testing-library/user-event` для component/shell tests;
- axe-compatible a11y assertion для placeholder;
- MSW как будущая GraphQL integration boundary, добавляется только вместе с первым operation test;
- Playwright config и browser dependency добавляются вместе с первым реальным E2E journey, а не пустым тестом;
- Storybook оценивается после появления набора shared components и не является foundation dependency.

На этом шаге обязательны lint, typecheck, unit/component tests и production build. Generated contract проверяется отдельной командой при доступной schema.

### 12. `ops/` и deployment boundary

`boundaries/ui/ops/README.md` фиксирует, что image/chart/runtime config будущего UI принадлежат `boundaries/ui/ops/`. Пустые Dockerfile/Helm manifests не создаются: hosting model, SSR decision, immutable asset strategy, CSP и public runtime config ещё не приняты. Это соответствует ADR 12 и не создаёт фиктивный deployable artifact.

### 13. ADR scope

Через корневой ADR CLI создаётся Accepted ADR `Astra UI as the Frontend Component Foundation`. Он фиксирует React/TypeScript, Tailwind/React Aria foundation, Astra façade и exit strategy, feature organization, mobile-first/a11y, generated GraphQL types и только публичный Cosmo GraphQL API.

ADR не принимает SPA/SSR, окончательный urql/Apollo выбор или authentication mechanism. В ADR явно отражается, что current Astra distribution package-based; термин `vendored` не используется для upstream package вопреки факту.

## Risks / Trade-offs

- [ТЗ ожидает vendored Astra source, а официальный web workflow package-based] → зафиксировать deviation, pin exact versions, изолировать imports façade-слоем и не смешивать upstream с нашим кодом.
- [Astra API или package ownership может измениться] → exact versions, lockfile, проверка лицензии/API при каждом update, локальная façade и React Aria exit strategy.
- [Vite SPA может не дать нужного SEO] → не реализовывать discovery до SEO/perf discovery; сохранить framework-neutral feature boundaries.
- [Provisional urql может не удовлетворить будущему cache] → не распространять client API по feature code; принимать окончательное решение на реальных operations.
- [Codegen зависит от ещё развивающейся public schema] → не создавать вымышленные product operations; schema source задавать явно, generation запускать при доступном contract.
- [Семантические tokens могут расходиться со стилями Astra] → маппить через локальный theme layer и проверять light/dark contrast на shared components.
- [Документация будущих flows может устареть] → считать GraphQL schema и feature-owned mapping executable source of truth по мере реализации; обновлять docs в тех же changes.
- [Отсутствие browser telemetry снижает diagnosability] → оставить reporter/transport seams; принять privacy и SDK policy до отправки production telemetry.

## Migration Plan

1. Создать `boundaries/ui/` scaffold и lockfile как независимое npm-приложение.
2. Добавить tokens, Astra façade, providers/router и placeholder; выполнить typecheck, tests и build.
3. Добавить codegen config без вымышленных product operations; проверить generation против текущей публичной BFF schema, если она доступна.
4. Добавить UI documentation, `ops/README.md`, корневую ссылку и Accepted ADR через CLI.
5. Rollback: удалить новый `boundaries/ui/` и связанные docs/ADR links; backend runtime и public GraphQL contract не меняются.

## Open Questions

- SPA остаётся production architecture или discovery подтверждает SSR/SSG?
- Какой GraphQL client утверждается после проверки cache, pagination, auth refresh и devtools: urql или Apollo?
- Какой authentication/session contract публикует BFF: OIDC token propagation, httpOnly cookie или BFF session?
- Cart является local-first или server-backed, и где проходит merge/identity boundary?
- Как публикуется canonical public GraphQL schema для frontend codegen и CI?
- Нужны ли subscriptions/SSE после измерения polling cost и latency?
- Когда shared component count оправдает Storybook?
- Какой browser error reporting/OTel SDK и consent/privacy policy допустимы?

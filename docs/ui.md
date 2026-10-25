# Customer UI architecture

## Граница приложения

Customer UI — самостоятельная application boundary в [`boundaries/ui/`](../boundaries/ui/README.md). Она предоставляет пользовательский web-интерфейс ресторанов, меню, корзины и заказов. Courier, restaurant и operator UI будут отдельными приложениями; до restaurant UI здесь живёт только экран открытия ресторана владельцем (см. «Feature organization»).

UI не является источником бизнес-правил, authorization или состояния заказа. Единственная business API boundary — публичный GraphQL API BFF на WunderGraph Cosmo: `browser → edge → Cosmo Router → BFF plugin → internal gateway → bounded context`. Решение зафиксировано в [ADR BFF о Cosmo](../boundaries/bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md).

## Design principles

- лёгкий consumer food-delivery interface, а не admin dashboard;
- mobile-first: полный customer flow должен работать с телефона;
- один primary action на экран;
- semantic HTML и доступные React Aria primitives;
- крупные food/restaurant images появятся только вместе с реальными или явно лицензированными assets;
- статусы заказа читаются без знания backend FSM или Temporal;
- визуальные значения задаются semantic tokens, а не локальными palette literals.

## Feature organization

Vertical UI slices: `browse-restaurants` (`/`), `view-restaurant` с корзиной (`/restaurants/:restaurantId`), `place-order` и `view-order` с отменой (`/orders/:orderId`); позже — `order-history`. Исключение из customer-границы — `manage-restaurant` (`/manage/restaurants/:restaurantId`), экран открытия ресторана владельцем: до отдельного restaurant UI он живёт здесь изолированной feature под префиксом `/manage/` без ссылок из customer-навигации и переносится целиком, когда restaurant UI появится (FR-001, FR-602). GraphQL documents, UI, tests и presentation mapping одной feature colocated внутри `src/features/<feature>/`. Если одна feature встраивает другую (`view-restaurant` показывает `place-order` под корзиной), она импортирует только публичный `index.ts` этой feature.

`src/shared/` содержит только реально переиспользуемые механизмы: UI façade, tokens, GraphQL transport/generated artifacts, config и i18n. Глобальные `components/`, `hooks/`, `services/` и `utils/` запрещены. Feature не импортирует внутренности другой feature.

## Headless UI и design tokens

[Headless UI](https://headlessui.com/) (`@headlessui/react`) задаёт поведение и accessibility semantics интерактивных компонентов, Tailwind CSS 4 и собственные semantic tokens — весь внешний вид ([ADR 38](adr/0038-headless-ui-as-the-frontend-component-foundation.md)). Headless UI импортируется только внутри `src/shared/ui/`; consumers используют локальную façade. Новый интерактивный примитив сначала берётся из Headless UI.

CSS variables разделены на semantic color, typography, spacing, radius, shadow и motion roles. Light и dark theme поддерживают system preference и будущий явный override. `prefers-reduced-motion` отключает non-essential transitions. Компоненты не содержат hardcoded palette values.

## Accessibility и responsive

Интерактивные элементы сохраняют keyboard activation, logical focus order, visible focus ring, semantic labels и screen-reader state. Dialog/Drawer/Alert Dialog в будущем строятся на Headless UI primitives, а не на `div`-эмуляции. Contrast проверяется в обеих темах.

Layout начинается с mobile viewport, не создаёт horizontal overflow и расширяется на tablet/desktop без изменения порядка задач пользователя. Accessibility проверяется component-тестами и axe; критические journeys позже проверяются реальным браузером.

## i18n и форматирование

Весь видимый текст проходит через typed messages. Locale поступает из validated runtime config. Валюта, числа, даты и plural rules создаются централизованно через `Intl`; ручная конкатенация локализуемых чисел и дат запрещена. Сейчас поддерживаются `ru` и `en`, fallback — `ru`.

## State и GraphQL

Server state принадлежит GraphQL client cache. Local UI state хранит только modal/selection/preferences, form state принадлежит форме. Глобальный store-копия GraphQL entities не вводится.

Корзина — local-first, одна на ресторан: feature `view-restaurant` хранит в `localStorage` только `menuItemId` и количество (ключ `nomnom.cart.v1.<restaurantId>`), без цен и персональных данных. Каждый доступ к storage обёрнут в `try/catch`, без storage корзина работает в памяти. Цены берутся из текущего меню, сумма корзины считается точно по десятичным строкам и показывается как предварительная; авторитетны состав и итог заказа из `placeOrder`. Серверная корзина и перенос между устройствами — вне текущего scope.

Operations colocated в `features/**/*.graphql`; документы и TypeScript-типы генерируются `graphql-codegen` client preset из публичной schema. Runtime client сейчас provisional urql и скрыт provider/transport boundary; окончательный выбор между urql и Apollo принимается после реальных cache, pagination, offline и auth-refresh требований.

Mocks MSW используются только в development/tests и обслуживают тот же публичный GraphQL contract: handler исполняет каждую operation через `graphql-js` против публичной BFF schema, а тест соответствия прогоняет все feature documents. Fixture data не выдаётся за production data и не переносит бизнес-правила во frontend. Сценарии моков описаны в [README UI](../boundaries/ui/README.md#msw-моки-customer-api).

## Authentication и authorization

Authentication mechanism остаётся открытым: предпочтителен OIDC с httpOnly cookie/BFF session; browser-readable long-lived token и `localStorage` требуют отдельного threat model.

UI может скрывать недоступные actions по данным публичного API только как UX. Это не security boundary: protected use case выполняет authoritative check на backend по [ADR 18 о SpiceDB](adr/0018-spicedb-for-authorization.md). Frontend не моделирует SpiceDB graph и не принимает permission decisions самостоятельно.

## Errors и loading

- validation error показывается у поля;
- business error формулируется пользовательским языком;
- infrastructure error получает generic recovery UI без gRPC, Kafka, Temporal или stack details;
- query loading использует layout-stable skeleton;
- mutation state локален и не блокирует всё приложение.

Mutations имеют состояния loading, disabled, success, business error, transport error и manual retry. Автоматический retry mutation запрещён без idempotency contract.

## Order status и обновление

Feature активного заказа владеет presentation mapping `backend state → UI status → localized label/icon/progress`. Несколько внутренних состояний могут схлопываться. Имена FSM и Temporal workflow не отображаются и не входят в публичный UI contract.

Базовая пользовательская последовательность: `Order received → Restaurant confirmed → Preparing → Courier assigned → Picked up → Delivered`.

Публичный контракт — `Order.status` (`OrderStatus`: `RECEIVED`, `CONFIRMED`, `PREPARING`, `READY_FOR_PICKUP`, `PICKED_UP`, `DELIVERED`, `CANCELLED`, `FAILED`), `Order.endReason` для неуспешного завершения и `milestones`. BFF вычисляет `OrderStatus` из состояния заказа и Fulfillment Facts `orders`; назначение курьера — milestone `COURIER_ASSIGNED`, а не статус. UI сопоставляет каждое значение enum с сообщением, icon и шагом прогресса и не показывает внутренние коды.

Обновления экрана заказа — GraphQL subscription `orderChanged` по SSE ([ADR 36](adr/0036-graphql-subscriptions-via-cosmo-streams.md)): `order(id)` загружает заказ, подписка присылает его после каждого изменения и закрывается на финальном статусе. При каждом (пере)подключении потока клиент перезапрашивает активные query, поэтому изменения между запросом и подпиской не теряются. Polling нет ни на одном экране: flow board так же следует за кухней (`kitchenChanged`), курьером (`courierChanged`) и чипами заказов (`orderChanged`). При ошибке остаётся ручное обновление. Смена статуса объявляется через `aria-live`.

Блок отмены показывается, пока не истёк `Order.cancellation.availableUntil`. `cancellation` BFF заполняет только после того, как `orders` начнёт публиковать окно отмены в `GetOrder`; до этого поле `null` и отмена не предлагается. `requestOrderCancellation` получает `requestId`, созданный на попытку и переиспользуемый при ручном retry.

Checkout attempt создаёт `idempotencyKey` (`PlaceOrderInput`) один раз через `crypto.randomUUID()` и переиспользует его при ручном retry после transport error; изменение корзины или адреса начинает новую попытку. Problems `placeOrder` с `menuItemIds` показываются у позиций корзины, без них — над формой; корзина очищается только после оформленного заказа.

## Observability

Browser telemetry — Grafana Faro Web SDK (`boundaries/ui/src/shared/telemetry/faro.ts`) по [ADR 9](adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md) и [observability conventions](observability.md): ошибки, Web Vitals, views по route pattern React Router, события `graphql_error` (вид операции, вид ошибки, request id) и fetch spans. Tracing добавляет W3C `traceparent` только к запросам на origin BFF, поэтому trace продолжается в Cosmo Router (`cors.allow_headers` BFF разрешает `traceparent`/`tracestate`). Приёмник — `faro.receiver` Alloy (`VITE_FARO_URL`); без URL и с MSW-моками SDK не инициализируется. Resource `service.name=customer-ui`, `service.namespace=nomnom`, `deployment.environment.name` из `VITE_DEPLOYMENT_ENVIRONMENT`.

Telemetry не содержит PII, payment details, auth secrets, полные GraphQL variables и high-cardinality resource identifiers: `beforeSend` заменяет id в URL страницы на шаблон route (`/orders/:orderId`) и отбрасывает query, console capture выключен, source maps Alloy не скачивает. Пользовательский error UI не раскрывает внутреннюю topology.

## Testing

- unit: config, i18n, formatters, mappings и redaction;
- component: React Testing Library, keyboard/focus и axe-compatible assertions;
- GraphQL integration: MSW против public contract fixtures;
- browser E2E: позднее, вместе с первым реальным journey `browse → add item → place order → view order`;
- visual regression: mobile/tablet/desktop, light/dark и reduced motion.

Playwright и Storybook не добавляются пустыми зависимостями. Storybook оценивается после появления набора shared components.

## Open decisions

- Vite SPA или SSR/SSG после SEO, metadata, Web Vitals и latency discovery;
- окончательный GraphQL client;
- OIDC/session и token storage contract;
- server-backed cart и merge/identity semantics после появления customer session;
- canonical publication public GraphQL schema для CI;
- browser error reporting/OTel SDK и privacy/consent policy;
- hosting, CSP, immutable asset и runtime-config delivery model.

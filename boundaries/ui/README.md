# Customer UI

Customer web-приложение BiteRoute находится в отдельной application boundary `boundaries/ui/`. Оно отвечает за пользовательские сценарии каталога ресторанов, меню, корзины и заказов, но не владеет backend-доменной моделью и не обращается к внутренним сервисам напрямую.

Customer journey работает на публичном GraphQL-контракте BFF: каталог открытых ресторанов на `/`, меню с корзиной и оформлением заказа на `/restaurants/:restaurantId` и экран заказа на `/orders/:orderId`. Каждый сценарий — отдельная feature со своими operations рядом с кодом (см. «Каталог, корзина и оформление» и «Экран заказа»).

## Запуск

Требуется Node.js 22 или новее.

```bash
cd boundaries/ui
npm ci
cp .env.example .env.local
npm run dev
```

Для локального mock transport в `.env.local` используется `VITE_ENABLE_MOCKS=true`. Единственный business endpoint приложения — `VITE_PUBLIC_GRAPHQL_URL`, адрес публичного GraphQL API BFF.

## MSW-моки customer API

Customer-схема BFF зафиксирована раньше resolvers (OpenSpec change [`add-customer-graphql-contract`](../../openspec/changes/add-customer-graphql-contract/)), поэтому UI развивается на MSW-моках той же схемы. Моки — только development и tests: в production bundle они не включаются, а данные fixtures демонстрационные.

- Публичная схема подключается alias `@public-schema` из `vite.config.ts` — тот же файл, что читает codegen (`GRAPHQL_SCHEMA` или `../bff/plugins/bff/src/schema.graphql`). Handler (`src/shared/graphql/mocks/handlers.ts`) валидирует и исполняет каждую operation через `graphql-js` против этой схемы, поэтому мок не может ответить формой, которую не вернёт BFF.
- In-memory backend — `createMockStore` (`mocks/store.ts`): у каждого экземпляра свои заказы, прогресс `order-live` и idempotency keys; clock подменяется в тестах.
- Тест соответствия (`mocks/contract.test.ts`) исполняет каждый document из `src/features/**/*.graphql` и `mocks/operations/*.graphql` против схемы с ответами моков; `schemaMismatches` (`mocks/conformance.ts`) находит поля вне схемы, неизвестные enum values, неверные scalars и отсутствующие non-null поля.

Сценарии заказа для `order(id)`:

| id | Сценарий |
| --- | --- |
| `order-received`, `order-preparing`, `order-picked-up`, `order-delivered` | заказ в соответствующем статусе; у `order-received` открыто окно отмены |
| `order-cancelled`, `order-restaurant-rejected`, `order-delivery-failed` | неуспешное завершение с `endReason` |
| `order-live` | каждый запрос продвигает заказ на следующий шаг; окно отмены открыто 5 минут |
| любой другой | `null` (отсутствующий или чужой заказ) |

Каталог: `restaurants` отдаёт только рестораны, принимающие заказы (как `ListRestaurants` по умолчанию), — семь открытых, то есть две страницы при размере страницы 6; `restaurant(id)` отдаёт и закрытую «Ночную пекарню» (`restaurant-night-bakery`). В меню «Кухни №8» (`restaurant-kitchen-8`) позиция «Гёдза с уткой» недоступна, у «Кофейни на углу» (`restaurant-corner-coffee`) меню не опубликовано.

Mutations: `placeOrder` идемпотентен по `idempotencyKey` и возвращает problems `RESTAURANT_NOT_ACCEPTING_ORDERS` и `ORDER_ITEM_UNAVAILABLE`; `requestOrderCancellation` идемпотентен по `requestId` и возвращает `ALREADY_CANCELLED`, `ORDER_ALREADY_FINAL` или `CANCELLATION_WINDOW_EXPIRED`.

## Каталог, корзина и оформление

На моках сценарий начинается с `http://localhost:4173/`.

- `browse-restaurants` (`src/features/browse-restaurants/`, route `/`): operation `BrowseRestaurants`; открытые рестораны страницами по 6 через `restaurants(first, after)`, «Показать ещё» запрашивает страницу после `endCursor` и переводит фокус на первый новый ресторан. Ошибка следующей страницы оставляет загруженные рестораны и предлагает повтор.
- `view-restaurant` (`src/features/view-restaurant/`, route `/restaurants/:restaurantId`): operation `ViewRestaurant`; меню, недоступные позиции видны с пометкой «Нет в наличии», но кнопка добавления неактивна. Закрытый ресторан показывает меню без возможности заказать, `null` — «Ресторан не найден».
- Корзина (`cart.ts`) — local-first, одна на ресторан: в `localStorage` под ключом `nomnom.cart.v1.<restaurantId>` хранятся только `menuItemId` и количество, цены всегда берутся из текущего меню. Каждый доступ к storage обёрнут в `try/catch`: без storage корзина живёт в памяти страницы. Позиции, которых больше нет в меню, при открытии удаляются.
- Сумма корзины подписана как предварительная и считается точно по десятичным строкам `Money.amount` через `BigInt` (`money.ts`), без float; авторитетная сумма — в заказе от `placeOrder`.
- `place-order` (`src/features/place-order/`): operation `PlaceOrder`, поле адреса и действие «Оформить заказ». `idempotencyKey` (`crypto.randomUUID()`) создаётся на попытку и переиспользуется при ручном повторе после transport error; изменение корзины или адреса начинает новую попытку с новым ключом; автоматического retry нет. Problems с `menuItemIds` показываются у этих позиций корзины, problems без позиций — над формой; корзина при этом не очищается. Успех очищает корзину и открывает `/orders/:id`. Другие features импортируют `place-order` только через `index.ts`.
- Адрес доставки не сохраняется в браузере.

## Экран заказа

Feature `view-order` (`src/features/view-order/`) — экран заказа на route `/orders/:orderId`, первый production-feature на публичном контракте. Открыть его на моках можно по адресу вида `http://localhost:4173/orders/order-live`.

- Operations `ViewOrder` и `RequestOrderCancellation` лежат в `order.graphql`; mapping `OrderStatus`, `OrderEndReason` и milestones на сообщения, icon и шаг прогресса — в `order-presentation.ts`.
- Экран показывает статус, прогресс с отметкой «Курьер назначен», состав и итог (`Intl` форматирует десятичную строку `Money.amount` без float), адрес и историю. При `CANCELLED`/`FAILED` прогресс заменяется сообщением по `endReason` без внутренних кодов и обещаний о возврате денег.
- `use-order-polling.ts`: запрос каждые 10 с, пока документ видим и статус не финальный; при ошибке интервал удваивается до 60 с, последние данные остаются на экране с кнопкой «Обновить».
- `cancel-order.tsx`: действие отмены видно, пока есть `cancellation.availableUntil`; подтверждение в `fieldset` переводит фокус на «Не отменять»; `requestId` создаётся на попытку и переиспользуется при ручном повторе, автоматического retry нет.
- С реальным BFF экран работает через resolvers слайса `ordertracking` ([README BFF](../bff/README.md#слайс-ordertracking)); позиция, которой уже нет в меню ресторана, приходит с пустым названием и показывается как «Позиция меню».
- Ответ `null` даёт «Заказ не найден», transport error и `Unimplemented` от BFF — generic recovery UI без технических деталей.

Основные проверки:

```bash
npm run format
npm run lint
npm run typecheck
npm test
npm run codegen
npm run build
npm run check
```

## Стек и структура

- React 19, TypeScript 7 и Vite 8;
- Tailwind CSS 4;
- React Aria Components как accessibility foundation;
- бесплатные web packages Astra `@astra-rn/react` и `@astra-rn/styles`;
- React Router;
- provisional urql client;
- GraphQL Code Generator с `client` preset;
- Vitest, Testing Library, axe-core и MSW.

Версии закреплены точно в [`package.json`](package.json) и [`package-lock.json`](package-lock.json). Biome используется для lint и format, как в других TypeScript-приложениях репозитория.

```text
src/
  app/                 bootstrap, providers, router и layout
  features/            vertical UI slices с их GraphQL operations
  shared/
    config/            validated public runtime config
    graphql/           client, generated types и mocks
    i18n/              typed messages и Intl formatters
    tokens/            semantic CSS tokens
    ui/                Astra façade и nomnom wrappers
  test/                общий test setup
ops/                   будущие runtime/deployment artifacts
```

Feature-specific code размещается внутри feature; другая feature использует её только через `index.ts`. Глобальные `components/`, `hooks/`, `services/` и `utils/` не создаются; код переносится в `shared/` только после реального повторного использования.

## GraphQL boundary

Допустимый runtime-путь: `browser → edge → Cosmo Router → BFF plugin → internal gateway → bounded context`. Решение о WunderGraph Cosmo закреплено в [локальном ADR BFF](../bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md).

UI не знает адреса backend boundaries и не импортирует gRPC, Kafka, Temporal или database clients. GraphQL operations хранятся рядом с feature, а TypeScript documents/types генерируются из публичной BFF schema:

```bash
npm run codegen
GRAPHQL_SCHEMA=https://example.test/graphql npm run codegen
```

Локальный default schema source — `../bff/plugins/bff/src/schema.graphql`. Generated files не редактируются вручную.

## Astra ownership

Актуальный web distribution Astra package-based: `@astra-rn/react` и `@astra-rn/styles` версии 1.0.1 публикуются с MIT metadata. Это отличается от исходного предположения о vendoring исходников. Pro и React Native/Expo animations не используются.

Feature и `app/` импортируют только façade из `src/shared/ui/`. Для обновления Astra нужно:

1. проверить latest-версии и license metadata через npm registry;
2. обновить exact pins и lockfile;
3. проверить API локальных wrappers;
4. выполнить `npm run check` и `npm run build`;
5. выполнить visual и accessibility regression check light/dark и mobile/desktop.

Exit strategy: заменить реализацию wrappers на React Aria Components, сохранив imports потребителей из `shared/ui/`. Второй component framework не добавляется.

Подробные conventions и open decisions описаны в [архитектуре UI](../../docs/ui.md).

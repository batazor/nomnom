# Customer UI

Customer web-приложение nomnom находится в отдельной application boundary `boundaries/ui/`. Оно отвечает за пользовательские сценарии каталога ресторанов, меню, корзины и заказов, но не владеет backend-доменной моделью и не обращается к внутренним сервисам напрямую.

Весь путь заказа виден на одном экране — доске заказа на `/` (`src/features/flow-board/`): колонки «Покупатель» (каталог, меню, корзина, оформление), «Кухня» (заказы ресторана по этапам, открытие ресторана) и «Курьер» (выход на линию, предложения, текущая доставка), сверху — заказы, оформленные с доски, и путь выбранного заказа. Дополнительные карточки — только выдвижная панель (детали заказа и отмена); других маршрутов нет, любой адрес ведёт на доску. Сценарии остальных features встраиваются в доску через их `index.ts` (см. «Доска заказа»).

## Запуск

Требуется Node.js 22 или новее.

```bash
cd boundaries/ui
npm ci
cp .env.example .env.local
npm run dev
```

Для локального mock transport в `.env.local` используется `VITE_ENABLE_MOCKS=true`. Единственный business endpoint приложения — `VITE_PUBLIC_GRAPHQL_URL`, адрес публичного GraphQL API BFF.

## Вход через Dex

С `VITE_OIDC_ISSUER` (в kind — `http://localhost:5556/dex`, port-forward `svc/dex` в `nomnom-data`) в каждой колонке доски есть кнопка «Войти: <роль>» ([ADR 35](../../docs/adr/0035-browser-login-via-dex-and-pkce.md)). `src/shared/auth/oidc.ts` выполняет authorization code + PKCE (public client `nomnom-ui`, scope `audience:server:client_id:nomnom-bff`, без `nonce`, `prompt=login`, чтобы в одном браузере войти несколькими пользователями); callback — route `/auth/callback`. Access token хранится только в памяти страницы и заменяет токен demo-пользователя этой роли (`AsRole`); он обновляется за минуту до истечения по refresh token (`offline_access`, ротация Dex), refresh token роли лежит в `sessionStorage`: перезагрузка восстанавливает вход, закрытие вкладки и «Выйти» его завершают. С `VITE_OIDC_DEMO_EMAIL_DOMAIN` и `VITE_OIDC_DEMO_PASSWORD` рядом с кнопкой показан демо-вход `<роль>@<домен> / <пароль>`. Redirect URI Dex разрешает для `localhost:4173` и `localhost:4180` (конфигурация `customer-ui-kind-login` в `.claude/launch.json`). Тестовые пользователи — `ops/kind/bootstrap/values.yaml` (`dex.users`).

## Доска заказа

- Доску ведёт один пользователь: в kind это `demo@nomnom.test` / `nomnom` через Dex (ADR 35), он покупатель, владелец, кухня и курьер. Без моков `VITE_OIDC_ISSUER` обязателен: без него приложение падает на старте. `AsRole` (`src/shared/session/roles.tsx`) всё равно даёт каждой колонке собственный urql client и кеш, но с токеном этого входа; на моках роли получают фиксированные токены.
- Колонки опрашивают BFF каждые 3 с, пока страница видима; шаг в одной колонке сразу обновляет остальные. Выбранный заказ подсвечивается в колонках кухни и курьера.
- Id оформленных с доски заказов хранятся в `localStorage` (`nomnom.board.orders.v1`, до 20); всё остальное читается из BFF.
- На узком экране колонки становятся вкладками.
- В моках `mocks/fulfillment.ts` ведёт кухню и курьера, как это делают restaurants и delivery: команды кухни двигают заказ ресторана, принятый заказ получает доставку, которая предлагается курьеру на линии, подтверждения курьера завершают её; каждый шаг меняет заказ покупателя.

## MSW-моки customer API

Customer-схема BFF зафиксирована раньше resolvers (OpenSpec change [`add-customer-graphql-contract`](../../openspec/changes/add-customer-graphql-contract/)), поэтому UI развивается на MSW-моках той же схемы. Моки — только development и tests: в production bundle они не включаются, а данные fixtures демонстрационные.

- Публичная схема подключается alias `@public-schema` из `vite.config.ts` — тот же файл, что читает codegen (`../bff/client-schema.graphql`). Handler (`src/shared/graphql/mocks/handlers.ts`) валидирует и исполняет каждую operation через `graphql-js` против этой схемы, поэтому мок не может ответить формой, которую не вернёт BFF. Subscriptions моков выполняются внутри процесса (`mocks/subscriptions.ts`, транспорт `subscriptions` клиента) против той же схемы и store: MSW в Node не держит потоковые ответы fetch. Мутации store будят подписчиков `orderChanged`, `kitchenChanged` и `courierChanged`, `order-live` сам продвигается на шаг каждые 4 с, пока за ним следят.
- In-memory backend — `createMockStore` (`mocks/store.ts`): у каждого экземпляра свои заказы, прогресс `order-live` и idempotency keys; clock подменяется в тестах.
- Тест соответствия (`mocks/contract.test.ts`) исполняет каждый document из `src/features/**/*.graphql` и `mocks/operations/*.graphql` против схемы с ответами моков; `schemaMismatches` (`mocks/conformance.ts`) находит поля вне схемы, неизвестные enum values, неверные scalars и отсутствующие non-null поля.

Сценарии заказа для `order(id)`:

| id | Сценарий |
| --- | --- |
| `order-received`, `order-preparing`, `order-picked-up`, `order-delivered` | заказ в соответствующем статусе; у `order-received` открыто окно отмены |
| `order-cancelled`, `order-restaurant-rejected`, `order-delivery-failed` | неуспешное завершение с `endReason` |
| `order-live` | пока на него есть подписка `orderChanged`, каждые 4 с переходит на следующий шаг; окно отмены открыто 5 минут |
| любой другой | `null` (отсутствующий или чужой заказ) |

Каталог: `restaurants` отдаёт только рестораны, принимающие заказы (как `ListRestaurants` по умолчанию), — семь открытых, то есть две страницы при размере страницы 6; `restaurant(id)` отдаёт и закрытую «Ночную пекарню» (`restaurant-night-bakery`). В меню «Кухни №8» (`restaurant-kitchen-8`) позиция «Гёдза с уткой» недоступна, у «Кофейни на углу» (`restaurant-corner-coffee`) меню не опубликовано.

Управление рестораном: `/manage/restaurants` — список ресторанов владельца (`myRestaurants`) со ссылками на экраны управления. Mock-пользователь владеет «Кухней №8» (`restaurant-kitchen-8`) и «Ночной пекарней» (`restaurant-night-bakery`); `openRestaurant`/`closeRestaurant` меняют состояние открытия в store (повтор — `changed = false`), и каталог и `placeOrder` видят новое состояние. Для другого существующего ресторана моки отвечают error `PermissionDenied`, для неизвестного — `NotFound` в форме Cosmo Router (`bffError`: код в `errors[].extensions.errors[].extensions.code`).

Mutations: `placeOrder` идемпотентен по `idempotencyKey` и возвращает problems `RESTAURANT_NOT_ACCEPTING_ORDERS` и `ORDER_ITEM_UNAVAILABLE`; `requestOrderCancellation` идемпотентен по `requestId` и возвращает `ALREADY_CANCELLED`, `ORDER_ALREADY_FINAL` или `CANCELLATION_WINDOW_EXPIRED`.

## Каталог, корзина и оформление

На моках сценарий начинается с `http://localhost:4173/`.

- `browse-restaurants` (`src/features/browse-restaurants/`, route `/`): operation `BrowseRestaurants`; открытые рестораны страницами по 6 через `restaurants(first, after)`, «Показать ещё» запрашивает страницу после `endCursor` и переводит фокус на первый новый ресторан. Ошибка следующей страницы оставляет загруженные рестораны и предлагает повтор.
- `view-restaurant` (`src/features/view-restaurant/`, route `/restaurants/:restaurantId`): operation `ViewRestaurant`; меню, недоступные позиции видны с пометкой «Нет в наличии», но кнопка добавления неактивна. Закрытый ресторан показывает меню без возможности заказать, `null` — «Ресторан не найден».
- Корзина (`cart.ts`) — local-first, одна на ресторан: в `localStorage` под ключом `nomnom.cart.v1.<restaurantId>` хранятся только `menuItemId` и количество, цены всегда берутся из текущего меню. Каждый доступ к storage обёрнут в `try/catch`: без storage корзина живёт в памяти страницы. Позиции, которых больше нет в меню, при открытии удаляются.
- Сумма корзины подписана как предварительная и считается точно по десятичным строкам `Money.amount` через `BigInt` (`money.ts`), без float; авторитетная сумма — в заказе от `placeOrder`.
- `place-order` (`src/features/place-order/`): operation `PlaceOrder` и действие «Оформить заказ». `idempotencyKey` (`crypto.randomUUID()`) создаётся на попытку и переиспользуется при ручном повторе после transport error; изменение корзины начинает новую попытку с новым ключом; автоматического retry нет. Problems с `menuItemIds` показываются у этих позиций корзины, problems без позиций — над формой; корзина при этом не очищается. Успех очищает корзину и открывает `/orders/:id`. Другие features импортируют `place-order` только через `index.ts`.

## Экран заказа

Feature `view-order` (`src/features/view-order/`) — экран заказа на route `/orders/:orderId`, первый production-feature на публичном контракте. Открыть его на моках можно по адресу вида `http://localhost:4173/orders/order-live`.

- Operations `ViewOrder` и `RequestOrderCancellation` лежат в `order.graphql`; mapping `OrderStatus`, `OrderEndReason` и milestones на сообщения, icon и шаг прогресса — в `order-presentation.ts`.
- Экран показывает статус, прогресс с отметкой «Курьер назначен», состав и итог (`Intl` форматирует десятичную строку `Money.amount` без float) и историю. При `CANCELLED`/`FAILED` прогресс заменяется сообщением по `endReason` без внутренних кодов и обещаний о возврате денег.
- `use-order-updates.ts`: query `ViewOrder` и subscription `OrderChanged` (fragment `ViewedOrder`) по SSE до финального статуса, без polling (ADR 36); при ошибке последние данные остаются на экране с кнопкой «Обновить». Клиент (`shared/graphql/client.ts`) перезапрашивает активные query при каждом подключении потока.
- `cancel-order.tsx`: действие отмены видно, пока есть `cancellation.availableUntil`; подтверждение в `fieldset` переводит фокус на «Не отменять»; `requestId` создаётся на попытку и переиспользуется при ручном повторе, автоматического retry нет.
- С реальным BFF экран работает через resolvers слайса `ordertracking` ([README BFF](../bff/README.md#слайс-ordertracking)); позиция, которой уже нет в меню ресторана, приходит с пустым названием и показывается как «Позиция меню».
- Ответ `null` даёт «Заказ не найден», transport error и `Unimplemented` от BFF — generic recovery UI без технических деталей.

## Экран управления рестораном

Feature `manage-restaurant` (`src/features/manage-restaurant/`, route `/manage/restaurants/:restaurantId`) — минимальный экран владельца ресторана, change [`add-restaurant-opening-management`](../../openspec/changes/add-restaurant-opening-management/). На моках: `http://localhost:4173/manage/restaurants/restaurant-night-bakery`.

- Operations `ManageRestaurant`, `OpenRestaurant` и `CloseRestaurant` лежат в `manage-restaurant.graphql`.
- Экран показывает «Открыт»/«Закрыт» и одно действие «Открыть ресторан»/«Закрыть ресторан». Пока команда выполняется, кнопка в pending и повторное нажатие не отправляет вторую команду; результат объявляется через `aria-live` и различает «уже был открыт/закрыт» (`changed = false`).
- Optimistic update нет: состояние меняется по payload команды, urql затем перезапрашивает query ресторана.
- `bff-error.ts` читает gRPC-код из ошибки BFF: `PermissionDenied` — «нет прав», `NotFound` — «ресторан не найден», `Unauthenticated` — «войдите в аккаунт владельца», остальное и transport error — generic сообщение; повтор только ручным нажатием (команды идемпотентны).
- Экран изолирован под `/manage/` и не связан с customer-навигацией: это исключение из границы customer UI до отдельного restaurant UI ([`docs/ui.md`](../../docs/ui.md#feature-organization)). Списка «мои рестораны» нет — экран открывается по ссылке с id ресторана. С реальным BFF нужен access token владельца; способ входа в браузере пока не выбран.

## Переключатель эмулятора

Feature `demo-simulator` (`src/features/demo-simulator/`) — переключатель «Эмулятор» справа в шапке (`app/layout.tsx`), change [`add-demo-simulator`](../../openspec/changes/add-demo-simulator/) (FR-607).

- Operations `DemoSimulator` и `SetSimulatorEnabled` лежат в `demo-simulator.graphql`; query выполняется один раз при загрузке, polling нет (ADR 36).
- Переключатель виден, только если `simulator` ответил без ошибки (оператор). При `PermissionDenied`, `Unauthenticated` и любой другой ошибке query шапка его не показывает.
- Пока mutation выполняется, переключатель disabled; optimistic update нет, состояние меняется только по ответу. Ошибка mutation — короткое сообщение под переключателем, состояние прежнее.
- В моках пользователь — оператор, Simulator выключен; `simulatorDeniedHandlers` из `mocks/handlers.ts` даёт вариант `PermissionDenied`.

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
- [Headless UI](https://headlessui.com/) `@headlessui/react` для поведения и accessibility компонентов;
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
    ui/                Headless UI façade и nomnom wrappers
  test/                общий test setup
ops/                   будущие runtime/deployment artifacts
```

Feature-specific code размещается внутри feature; другая feature использует её только через `index.ts`. Глобальные `components/`, `hooks/`, `services/` и `utils/` не создаются; код переносится в `shared/` только после реального повторного использования.

## GraphQL boundary

Допустимый runtime-путь: `browser → edge → Cosmo Router → BFF plugin → internal gateway → bounded context`. Решение о WunderGraph Cosmo закреплено в [локальном ADR BFF](../bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md).

UI не знает адреса backend boundaries и не импортирует gRPC, Kafka, Temporal или database clients. GraphQL operations хранятся рядом с feature, а TypeScript documents/types генерируются из публичной BFF schema:

```bash
npm run codegen
```

Schema source — `../bff/client-schema.graphql`: композиция subgraphs BFF, включая subscriptions (`npm run compose` в `boundaries/bff`). Generated files не редактируются вручную.

## Headless UI ownership

Интерактивные компоненты строятся на `@headlessui/react` (exact pin, [ADR 38](../../docs/adr/0038-headless-ui-as-the-frontend-component-foundation.md)); внешний вид — собственные tokens и CSS. Feature и `app/` импортируют только façade из `src/shared/ui/`. Новый интерактивный примитив сначала ищется в Headless UI и оборачивается в `shared/ui/`. Для обновления Headless UI нужно:

1. проверить latest-версию через npm registry;
2. обновить exact pin и lockfile;
3. проверить API локальных wrappers;
4. выполнить `npm run check` и `npm run build`;
5. выполнить visual и accessibility regression check light/dark и mobile/desktop.

Подробные conventions и open decisions описаны в [архитектуре UI](../../docs/ui.md).

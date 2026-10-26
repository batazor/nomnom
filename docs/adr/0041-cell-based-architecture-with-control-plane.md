# 41. Cell-based architecture: control plane, ячейки и маршрутизация по Market

Date: 2026-10-26

## Status

Accepted

Опирается на [2. Bounded contexts](0002-define-bounded-contexts-and-implementation-languages.md),
[4. Kafka](0004-use-event-driven-architecture-with-apache-kafka.md),
[12. Helm charts](0012-helm-charts-per-deployable-application.md),
[15. Internal gateway](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md),
[18. SpiceDB](0018-spicedb-for-authorization.md),
[32. User identity через BFF](0032-user-identity-via-bff-signed-assertions.md),
[36. GraphQL subscriptions](0036-graphql-subscriptions-via-cosmo-streams.md) и
[40. Кеширование чтений](0040-application-caching-policy.md). Уточняет точку входа из
[локального ADR 2 BFF](../../boundaries/bff/docs/adr/0002-bff-as-the-client-entry-point.md).

## Context

Вся система — один экземпляр: один Kafka, одна PostgreSQL/MongoDB на контекст, один набор
сервисов. Отказ или плохой деплой любого компонента задевает всех пользователей, а рост нагрузки
масштабируется только вертикально — раздуванием тех же кластеров.

Предметная область доставки еды естественно делится по городам: ресторан, его курьеры, клиент и
заказ почти всегда находятся в одном городе, а межгородских транзакций нет. Ресторан как ключ не
подходит — курьер возит заказы многих ресторанов, клиент как ключ тоже — клиент заказывает из любого
ресторана. Понятия tenant (B2B-клиент со своими данными) в модели нет; «tenant-изоляция» в ADR BFF
означает проверку принадлежности ресурса владельцем и остаётся авторизацией, а не маршрутизацией.

## Decision

Систему делить на **control plane** и **data plane из ячеек (Cells)**; ключ разбиения — **Market**.

### Термины

- **Market** — город, в котором работает доставка: рестораны, курьеры и заказы одного Market
  обслуживаются вместе. Идентификатор `market_id` — slug `^[a-z][a-z0-9-]{1,31}$` (`berlin`),
  неизменяемый. У Market есть название и валюта ISO 4217.
- **Cell** — полный изолированный экземпляр data plane: `orders`, `restaurants`, `delivery`,
  `payments`, их хранилища, Kafka, Debezium, Valkey, Temporal и internal gateway ячейки.
  Идентификатор `cell_id` — slug (`cell-1`).
- **Market Placement** — размещение Market в одной Cell. Market в каждый момент обслуживается
  ровно одной Cell; Cell обслуживает один или несколько Market.

### Control plane

Глобальный, один на систему: boundary `boundaries/controlplane` (Go, PostgreSQL), gRPC API
`nomnom.controlplane.v1.ControlPlaneService`.

- Владеет реестром Cell (`cell_id`, адрес internal gateway ячейки), реестром Market и их
  Market Placement.
- Записи — `RegisterCell`, `CreateMarket` — только для `platform#manage_markets` (оператор,
  [ADR 18](0018-spicedb-for-authorization.md)). Повтор с теми же значениями идемпотентен, с другими —
  `ALREADY_EXISTS`. Market создаётся сразу размещённым в существующей Cell.
- Чтения — `ListMarkets` (Market для выбора в UI) и `GetPlacementMap` (Market → Cell и адрес её
  gateway) — для service identity BFF.
- Перенос Market между Cell (миграция данных) в API не входит: горизонтальный рост — новый Market в
  новой Cell. Процедура переноса будет отдельным решением.
- Глобальными, вне ячеек, остаются также identity (Dex, [ADR 35](0035-browser-login-via-dex-and-pkce.md)),
  BFF и SpiceDB.

**Control plane не находится в горячем пути.** Ячейки не обращаются к control plane вообще: ни при
старте, ни на запрос. BFF держит Placement Map в памяти с TTL (`BFF__PLACEMENT_TTL`) и обновляет её в
фоне; неудачное обновление оставляет последнюю полученную карту (static stability) и пишется в
метрики и лог. Карта меняется только записями оператора, поэтому устаревшая карта не направляет
запрос в неверную Cell для уже размещённых Market. Пока карта ни разу не получена, запросы data
plane завершаются `UNAVAILABLE`. Недоступный control plane останавливает только изменение топологии.

### Data plane и `market_id`

`market_id` — обязательный атрибут агрегатов, заданный при создании и неизменяемый:

| Контекст | Агрегат | Откуда берётся |
| --- | --- | --- |
| `restaurants` | Restaurant | `RegisterRestaurant.market_id` |
| `restaurants` | Restaurant Order (Kitchen Order) | Restaurant, получивший заказ |
| `orders` | Order | Restaurant на Place Order (catalog snapshot) |
| `delivery` | Courier | `RegisterCourier.market_id` |
| `delivery` | Delivery | `CreateDelivery.market_id` (Market заказа) |
| `payments` | Payment | `CreatePayment.market_id` (Market заказа) |

- Списочные чтения принимают обязательный `market_id` и возвращают только данные этого Market:
  `ListRestaurants`, `ListOwnedRestaurants`, `ListCustomerOrders`, `GetCourierPool`.
- Курьеру предлагаются и назначаются только Delivery его Market; Place Order и Restaurant Order не
  пересекают границу Market.
- Каждое Integration Event несёт `market_id`. Partition key не меняется: Kafka — своя в каждой Cell,
  порядок по aggregate сохраняется как прежде.
- Синхронные вызовы между контекстами остаются внутри Cell через её internal gateway
  ([ADR 15](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)).
  Вызовов из одной Cell в другую нет.
- Пустой или неизвестный по формату `market_id` — `INVALID_ARGUMENT`. Ячейка не проверяет, что Market
  размещён в ней: туда её направляет router, а ресурс чужого Market в ней не найдётся.
- Данные, созданные до введения Market, принадлежат стартовому Market `berlin`: миграции схемы
  ([ADR 38](0038-schema-migrations-at-process-startup.md)) один раз проставляют его явно и делают
  поле обязательным.

### Маршрутизация

Router ячеек — BFF; отдельного прокси нет.

- Клиент выбирает Market и передаёт его в каждом запросе заголовком `X-Nomnom-Market`. Cosmo Router
  пробрасывает его в plugin как gRPC metadata `x-nomnom-market`, как `Authorization`.
- Заголовок — подсказка маршрутизации, а не утверждение об identity: права на действие и
  принадлежность ресурса по-прежнему проверяет сервис-владелец ([ADR 32](0032-user-identity-via-bff-signed-assertions.md)).
- Соединение BFF с контекстом — `grpc.ClientConnInterface`, выбирающий на каждый вызов gateway
  Cell по Market из metadata. Нет заголовка, неверный формат или Market не размещён —
  ошибка клиенту (`INVALID_ARGUMENT` / `NOT_FOUND`), а не Cell «по умолчанию».
- Списочные операции BFF передают `market_id` из заголовка в запрос владельцу.
- Control plane BFF вызывает через internal gateway по адресу `BFF__INTERNAL_GATEWAY_ADDR`.
- GraphQL `markets` отдаёт список Market для выбора в UI.

### Развёртывание

- В kind один control plane, одна Cell `cell-1` и один Market `berlin`. Gateway `cell-1` — текущий
  internal gateway; он же обслуживает маршруты control plane (`controlplane-v1-reads`,
  `controlplane-v1-commands`). `cell-1` и `berlin` регистрирует Job seed chart `ops/kind/demo`
  (`files/seed.json`) от имени оператора через `RegisterCell` и `CreateMarket`, идемпотентно; не
  chart bootstrap ([ADR 34](0034-declarative-kind-bootstrap.md)): он ставится до charts
  приложений, и control plane к нему ещё не запущен. Bootstrap даёт control plane PostgreSQL,
  preshared key SpiceDB и JWKS.
- Новая Cell — те же Helm charts ([ADR 12](0012-helm-charts-per-deployable-application.md)) с values
  ячейки в своих неймспейсах и свой Gateway; затем `RegisterCell` и `CreateMarket` в ней.
- Подписки Cosmo Streams ([ADR 36](0036-graphql-subscriptions-via-cosmo-streams.md)) читают Kafka
  `cell-1`; вторая Cell добавляет свой provider в конфигурацию Router.

## Consequences

Отказ, перегрузка или неудачный деплой Cell задевает только её Market; рост — добавление Cell, а не
раздувание общих кластеров. Каждая Cell — один и тот же размер, проверенный нагрузочным прогоном
([ADR 26](0026-k6-for-performance-testing.md)).

Цена: эксплуатация умножается на число Cell; каждый клиентский запрос обязан нести Market; история
заказов и пул курьеров видны в пределах Market. BFF и control plane становятся глобальными
компонентами и должны быть проще и надёжнее ячеек: BFF — stateless и горизонтально масштабируется,
control plane не участвует в обработке запросов. Перенос Market между Cell, глобальная аналитика по
всем Market и подписки из нескольких Cell — отдельные решения, когда появится вторая Cell.

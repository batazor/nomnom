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
- Переноса Market между Cell в API нет; как он будет устроен — в «Стратегии расширения».
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
- Courier Pool — у каждого Market свой: `CourierPool.id = market_id`, Change Notification пула несёт
  ключ Market, а subscription `courierPoolChanged(marketId)` фильтрует по нему
  ([ADR 36](0036-graphql-subscriptions-via-cosmo-streams.md)).
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
- Второй Cell в kind нет и не будет: стенд её не вытянет, а стратегия расширения ниже не
  реализуется и не проверяется на стенде.
- Подписки Cosmo Streams ([ADR 36](0036-graphql-subscriptions-via-cosmo-streams.md)) читают Kafka
  `cell-1`.

### Стратегия расширения

Не реализуется и не проверяется сейчас; записана, чтобы текущие решения её не закрывали.

**Рост — новый Market в новой Cell.** Те же Helm charts
([ADR 12](0012-helm-charts-per-deployable-application.md)) с values ячейки в своих неймспейсах и свой
Gateway; затем `RegisterCell` и `CreateMarket` в ней.

**Market не делится между Cell.** Город, который перерос типовой размер Cell, получает свою Cell
большего размера; масштабирование внутри неё — дело каждого сервиса (реплики, партиции Kafka,
шардирование своего хранилища). Второго измерения ключа (районов) нет: делить город между Cell
значит маршрутизировать по ресурсу, а не по Market, и собирать чтения из нескольких Cell — это
дороже, чем большая Cell.

**Перенос Market между Cell (Market Migration)** — когда понадобится разгрузить Cell или вынести
город в свою. С окном обслуживания города, а не живой: Market — один город в одном часовом поясе,
ночное окно в минуты допустимо, а живой перенос (CDC-копия, двойная запись, перенос незавершённых
workflow Temporal) многократно сложнее. Шаги по фазам AWS (clone → flip → redirect → forget):

1. **Drain.** Исходная Cell перестаёт принимать Place Order этого Market; ждать, пока у Market не
   останется незавершённых Order, Kitchen Order, Delivery, Payment и workflow Temporal. Срок
   ожидания ограничен; истёк — перенос отменяется.
2. **Freeze.** Исходная Cell отклоняет все команды и обработку событий Market.
3. **Copy.** Каждый контекст выгружает агрегаты Market, целевая Cell загружает их замороженными;
   данных Market в ней до этого быть не должно. Загрузка не публикует событий, read models
   пересобираются из агрегатов. Outbox, Inbox, Kafka, кеш Valkey и история Temporal не
   копируются — после дренажа незавершённого в них нет.
4. **Verify.** Число записей и контрольная сумма по каждому типу агрегата совпадают.
5. **Switch.** Целевая Cell открывает Market, Placement переключается на неё, исходная отклоняет
   все запросы Market (`UNAVAILABLE`).
6. **Forget.** Через заданный срок исходная Cell удаляет данные Market.

До Switch перенос отменяется без потерь, после — только обратным переносом.

Что для этого понадобится:

- **Market Hold** — ограничение `draining` / `frozen` / `moved`, которое хранит и проверяет сам
  контекст, а не router: BFF с устаревшей картой (static stability) может прислать запрос в
  прежнюю Cell, и отклонить его должна она.
- Служебный API переноса в каждом контексте — Hold, статус дренажа, выгрузка, загрузка,
  контрольная сумма, удаление, — только для service identity control plane
  ([ADR 27](0027-service-identity-via-projected-serviceaccount-tokens.md)).
- Оркестрация переноса в control plane — workflow Temporal в своём Temporal; ячейки не знают друг
  о друге, данные идут через worker control plane.
- Подписки не из Kafka одной Cell: provider у поля subscription один, а Market после переноса
  публикует уведомления в другой Cell — например, глобальный Kafka уведомлений, куда зеркалируются
  топики Change Notification всех Cell.

## Consequences

Отказ, перегрузка или неудачный деплой Cell задевает только её Market; рост — добавление Cell, а не
раздувание общих кластеров. Типовая Cell — один и тот же размер, проверенный нагрузочным прогоном
([ADR 26](0026-k6-for-performance-testing.md)); город крупнее неё получает свою Cell большего размера.

Цена: эксплуатация умножается на число Cell; каждый клиентский запрос обязан нести Market; история
заказов и пул курьеров видны в пределах Market. BFF и control plane становятся глобальными
компонентами и должны быть проще и надёжнее ячеек: BFF — stateless и горизонтально масштабируется,
control plane не участвует в обработке запросов. Перенос Market не реализован: до него Market
навсегда остаётся в Cell, где создан. Перенос будет стоить простоя города на время дренажа и
копирования и обязательной выгрузки и загрузки для каждого агрегата. Глобальная аналитика по всем
Market — отдельное решение.

## References

- [WSO2 — Cell-Based Architecture: A Decentralized Reference Architecture for Cloud Native Applications](https://github.com/wso2/reference-architecture/blob/master/reference-architecture-cell-based.md)

  Термин «cell» у WSO2 шире нашего: там Cell — единица разбиения по bounded context и команде
  (у нас это boundary, [ADR 2](0002-define-bounded-contexts-and-implementation-languages.md)), а
  здесь Cell — полная копия data plane для набора Market. Общее, что берём:
  - gateway — единственная точка входа в Cell, точка политик и наблюдаемости: internal gateway
    ячейки ([ADR 15](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md));
  - разделение на data plane внутри ячеек и глобальные control/management plane вне их;
  - федеративная безопасность: IdP (Dex) и SpiceDB в глобальном контуре, а не STS в каждой Cell;
  - Cell — неизменяемая единица развёртывания по версионированному процессу (Helm charts,
    [ADR 12](0012-helm-charts-per-deployable-application.md)), со своим масштабированием и
    защитой от перегрузки.

  Межъячеечного трафика (east/westbound по терминам WSO2) у нас нет: ячейки однотипны и
  изолированы, связь между контекстами остаётся внутри Cell.
- [AWS — Reducing the Scope of Impact with Cell-Based Architecture](https://docs.aws.amazon.com/wellarchitected/latest/reducing-scope-of-impact-with-cell-based-architecture/what-is-a-cell-based-architecture.html)

  Тот же смысл Cell, что и здесь: полная изолированная копия workload, разбиение по partition key,
  тонкий router и control plane вне горячего пути. Соответствие:
  - partition key по «зерну» предметной области — Market; алгоритм — full mapping (явная
    Placement Map), он же даёт override конкретного Market на конкретную Cell;
  - control plane — CP (отказывает, но не отдаёт неверного), data plane и router — AP: router
    держит карту в памяти и работает на устаревшей при недоступном control plane (static stability);
  - нет вызовов между Cell и общих хранилищ у ячеек;
  - текущий стек — «cell zero» (`cell-1`), router добавлен поверх;
  - Cell фиксированного, проверенного нагрузкой размера; рост — новые Cell (scale-out).

  Расхождения: router — BFF, а не «тончайший слой без бизнес-логики»; SpiceDB и Dex — общие для
  всех Cell зависимости в горячем пути; Market не делится между Cell — крупный город получает Cell
  большего размера вместо второго измерения ключа; перенос Market и вторая Cell только описаны в
  стратегии расширения, а не заложены с первого дня; перенос — с окном обслуживания, а не online;
  развёртывание волнами по Cell, лимиты и load shedding на Cell и метки `cell_id` в телеметрии
  пока не введены.

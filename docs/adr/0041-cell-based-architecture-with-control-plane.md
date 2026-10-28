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
[43. Кеширование чтений](0043-application-caching-policy.md). Уточняет точку входа из
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
Router ячеек — публичный gateway; всё, что обслуживает клиентский запрос, кроме него, Dex и SpiceDB,
находится внутри Cell.

### Термины

- **Market** — город, в котором работает доставка: рестораны, курьеры и заказы одного Market
  обслуживаются вместе. Идентификатор `market_id` — slug `^[a-z][a-z0-9-]{1,31}$` (`berlin`),
  неизменяемый. У Market есть название, валюта ISO 4217 и часовой пояс IANA (`Europe/Berlin`):
  клиент и ресторан всегда в одном Market, поэтому окна и сроки заказа показываются в поясе Market.
- **Cell** — полный изолированный экземпляр data plane: Cosmo Router с plugin BFF, `orders`,
  `restaurants`, `delivery`, `payments`, их хранилища, Kafka, Debezium, Valkey, Temporal и internal
  gateway ячейки. Идентификатор `cell_id` — slug (`cell-1`).
- **Market Placement** — текущая Cell Market: в ней создаются все новые агрегаты Market. Cell
  обслуживает один или несколько Market.
- **Previous Placement** — Cell, в которой Market был размещён до переноса (Market Migration). Она
  навсегда хранит и обслуживает агрегаты Market, созданные в ней, но не создаёт новых.

### Control plane

Глобальный, один на систему: boundary `boundaries/controlplane` (Go, PostgreSQL), gRPC API
`nomnom.controlplane.v1.ControlPlaneService`.

- Владеет реестром Cell (`cell_id` и Kubernetes Service её Cosmo Router — точки входа Cell),
  реестром Market, их Market Placement и Previous Placement.
- Записи — `RegisterCell`, `CreateMarket` — только для `platform#manage_markets` (оператор,
  [ADR 18](0018-spicedb-for-authorization.md)). Повтор с теми же значениями идемпотентен, с другими —
  `ALREADY_EXISTS`. Market создаётся сразу размещённым в существующей Cell.
- `ArchiveMarket` (тоже `platform#manage_markets`) архивирует Market: он уходит из `ListMarkets`, из
  списка Market и с публичного gateway (его `HTTPRoute` удаляется), строка реестра остаётся, и
  `market_id` больше не создаётся (`ALREADY_EXISTS`, `MARKET_ARCHIVED`). Повтор — успех с
  `archived = false`. Удаления Market нет: id не переиспользуется. Так прогоны kind e2e, которым
  нужен свой Market на прогон, убирают его в cleanup.
- Чтение `ListMarkets` — для оператора; архивные Market не возвращаются.
- **Маршруты публичного gateway пишет control plane.** Из реестра он строит ресурсы Gateway API в
  неймспейсе публичного gateway и применяет их server-side apply: `HTTPRoute` на каждое размещение
  Market (см. «Маршрутизация») и список Market для выбора в UI — `ConfigMap`, который Envoy отдаёт
  сам по `GET /markets` через `HTTPRouteFilter` `directResponse`. Права ServiceAccount control plane
  — только запись `HTTPRoute`, `HTTPRouteFilter` и `ConfigMap` в этом неймспейсе и удаление своих
  `HTTPRoute` Market, которых нет в реестре (архивных). Запись оператора
  сначала фиксируется в БД, затем применяются ресурсы; ошибка применения возвращается оператору как
  `UNAVAILABLE`, и повтор той же команды (идемпотентный) применяет их снова. При старте control
  plane один раз приводит ресурсы к реестру.
- Глобальными, вне ячеек, остаются также identity (Dex, [ADR 35](0035-browser-login-via-dex-and-pkce.md))
  и SpiceDB.

**Control plane не находится в горячем пути.** Ни ячейки, ни gateway не обращаются к control plane
на запрос: маршруты и список Market — ресурсы Kubernetes, Envoy держит последнюю полученную
конфигурацию и при недоступном control plane или контроллере Envoy Gateway продолжает маршрутизировать
по ней (static stability). Недоступный control plane останавливает только изменение топологии.

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
- Синхронные вызовы между контекстами и вызовы BFF остаются внутри Cell через её internal gateway
  ([ADR 15](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)).
  Вызовов из одной Cell в другую нет.
- Пустой или неизвестный по формату `market_id` — `INVALID_ARGUMENT`. Ячейка не проверяет, что Market
  размещён в ней: туда её направляет router, а ресурс чужого Market в ней не найдётся.
- Данные, созданные до введения Market, принадлежат стартовому Market `berlin`: миграции схемы
  ([ADR 42](0042-schema-migrations-at-process-startup.md)) один раз проставляют его явно и делают
  поле обязательным.

### Маршрутизация

Router ячеек — публичный gateway (Envoy Gateway): тонкий слой без бизнес-логики, который по пути
запроса выбирает Cell. Market передаётся в пути, а не заголовком клиента: путь адресует и Market
Placement, и Previous Placement (`@{cell_id}`) одинаково для запросов, SSE-подписок
([ADR 36](0036-graphql-subscriptions-via-cosmo-streams.md)) и ссылок, а заголовок Market Cell
получает от gateway, а не от клиента. Искать Market по пользователю или ресурсу нельзя: у
пользователя нет одного Market, а глобальный индекс ресурсов вернул бы общее хранилище в горячий путь.

- Клиент выбирает Market и обращается к API по пути `/m/{market_id}/…` (`/m/berlin/graphql`).
- На каждое размещение Market — `HTTPRoute` с совпадением по префиксу пути и backend — Cosmo Router
  Cell:
  - `/m/{market_id}` → Market Placement;
  - `/m/{market_id}@{cell_id}` → Previous Placement, для агрегатов, созданных там до переноса.

  Маршрут снимает префикс (`URLRewrite`) и выставляет заголовок `X-Nomnom-Market: {market_id}`
  (`RequestHeaderModifier` `set` — значение клиента перезаписывается). Cell видит обычный путь API, а
  Cosmo Router пробрасывает заголовок в plugin как gRPC metadata `x-nomnom-market`, как
  `Authorization`. Ссылку на Service в неймспейсе Cell разрешает `ReferenceGrant` из chart Cell.
- Неизвестный Market или Cell не совпадает ни с одним маршрутом: gateway отвечает `404`, Cell «по
  умолчанию» нет.
- `X-Nomnom-Market` — подсказка маршрутизации, а не утверждение об identity: права на действие и
  принадлежность ресурса по-прежнему проверяет сервис-владелец ([ADR 32](0032-user-identity-via-bff-signed-assertions.md)).
  BFF без заголовка или с заголовком неверного формата отвечает `INVALID_ARGUMENT`.
- Списочные операции BFF передают `market_id` из заголовка в запрос владельцу; вызовы BFF идут в
  internal gateway своей Cell (`BFF__INTERNAL_GATEWAY_ADDR`).
- UI получает список Market из `GET /markets`: `market_id`, название, валюта, часовой пояс и
  Previous Placement каждого Market. По Previous Placement UI строит пути `@{cell_id}` к заказам,
  созданным до переноса, и собирает историю заказов из всех размещений Market.

### Развёртывание

- В kind один control plane, одна Cell `cell-1` и один Market `berlin`. Публичный API Cosmo Router
  `cell-1` публикуется через публичный gateway по маршруту `/m/berlin`. Internal gateway `cell-1` —
  текущий internal gateway; он же обслуживает маршруты control plane (`controlplane-v1-reads`,
  `controlplane-v1-commands`). `cell-1` и `berlin` регистрирует Job seed chart `ops/kind/demo`
  (`files/seed.json`) от имени оператора через `RegisterCell` и `CreateMarket`, идемпотентно; не
  chart bootstrap ([ADR 34](0034-declarative-kind-bootstrap.md)): он ставится до charts
  приложений, и control plane к нему ещё не запущен. Bootstrap даёт control plane PostgreSQL,
  preshared key SpiceDB и JWKS.
- Второй Cell в kind нет и не будет: стенд её не вытянет, а стратегия расширения ниже не
  реализуется и не проверяется на стенде.
- Подписки Cosmo Streams ([ADR 36](0036-graphql-subscriptions-via-cosmo-streams.md)) Cosmo Router
  каждой Cell читают Kafka своей Cell.

### Стратегия расширения

Не реализуется и не проверяется сейчас; записана, чтобы текущие решения её не закрывали.

**Рост — новый Market в новой Cell.** Те же Helm charts
([ADR 12](0012-helm-charts-per-deployable-application.md)) с values ячейки в своих неймспейсах;
затем `RegisterCell` и `CreateMarket` в ней. Cell в другом кластере — backend маршрута через
`Backend` Envoy Gateway вместо Service.

**Market не делится между Cell.** Город, который перерос типовой размер Cell, получает свою Cell
большего размера; масштабирование внутри неё — дело каждого сервиса (реплики, партиции Kafka,
шардирование своего хранилища). Второго измерения ключа (районов) нет: делить город между Cell
значит маршрутизировать по ресурсу, а не по Market, и собирать чтения из нескольких Cell — это
дороже, чем большая Cell.

**Перенос Market между Cell (Market Migration)** — когда понадобится разгрузить Cell или вынести
город в свою. Без простоя: новые заказы сразу идут в целевую Cell, начатые дорабатывают в исходной,
а созданные в ней агрегаты остаются там навсегда. Копировать незавершённые workflow Temporal,
Outbox, Inbox и Kafka не нужно — их ничто не переносит.

1. **Copy.** Restaurant (с каталогом и владельцами), Cook и Courier Market копируются в целевую
   Cell. На время копирования исходная Cell отклоняет команды управления ими (каталог, Hire Cook,
   Register Courier и подобные), заказы продолжают приниматься. Скопированные Cook и Courier в
   целевой Cell не могут выйти на линию, пока не переведены (шаг 3). Загрузка не публикует событий,
   read models пересобираются из агрегатов.
2. **Switch.** Control plane переключает Market Placement на целевую Cell, исходная становится
   Previous Placement: маршрут `/m/{market_id}` ведёт в целевую, `/m/{market_id}@{source}` — в
   исходную. Исходная Cell отклоняет создание новых агрегатов Market (Place Order, Register
   Restaurant, Register Courier, Hire Cook) и команды управления Restaurant; команды над уже
   существующими в ней Order, Kitchen Order, Delivery и Payment выполняются как обычно.
3. **Перевод людей — по окончании смены.** Сменой считается отрезок от выхода на линию до ухода с
   неё; уход с линии и сейчас отклоняется, пока у человека активная работа.
   - Cook переходит в целевую Cell, когда уходит с линии в исходной (Take Cook Off Line), — если в
     исходной Cell у его ресторана не осталось Kitchen Order в `QUEUED`. Иначе следующую смену он
     работает в исходной Cell.
   - Courier переходит так же, когда уходит с линии в исходной, — если в исходной Cell у Market не
     осталось Delivery, ждущих курьера.
   - После перевода Cook или Courier выходит на линию только в целевой Cell. Переход переводит
     worker переноса control plane: он получает уход с линии из исходной Cell и открывает человека
     в целевой.
   - Cook и Courier других ресторанов и Market исходной Cell перенос не затрагивает.
4. **Done.** У Market в исходной Cell не осталось незавершённых агрегатов, все Cook и Courier
   переведены. Исходная Cell остаётся Previous Placement навсегда: хранит и отдаёт свои заказы и
   выполняет команды над ними (например, возврат платежа), но новых агрегатов Market не создаёт.

До Switch перенос отменяется без потерь, после — только обратным переносом.

Что для этого понадобится:

- **Market Hold** — ограничение `copying` / `previous`, которое хранит и проверяет сам контекст, а
  не router: оно отклоняет создание новых агрегатов и команды управления даже на запросе, пришедшем
  по старому маршруту.
- Состояние «переведён» у Cook и Courier в обеих Cell: в исходной — больше не выходит на линию, в
  целевой — до перевода не выходит на линию.
- Служебный API переноса в каждом контексте — Hold, выгрузка и загрузка Restaurant, Cook и Courier,
  контрольная сумма, перевод человека, статус незавершённых агрегатов, — только для service
  identity control plane ([ADR 27](0027-service-identity-via-projected-serviceaccount-tokens.md)).
- Оркестрация переноса в control plane — workflow Temporal в своём Temporal; ячейки не знают друг
  о друге, данные и переводы идут через worker control plane.

## Consequences

Отказ, перегрузка или неудачный деплой Cell, включая её Cosmo Router и BFF, задевает только её
Market; рост — добавление Cell, а не раздувание общих кластеров. Типовая Cell — один и тот же размер,
проверенный нагрузочным прогоном ([ADR 26](0026-k6-for-performance-testing.md)); город крупнее неё
получает свою Cell большего размера.

Цена: эксплуатация умножается на число Cell; каждый клиентский путь несёт Market; история заказов и
пул курьеров видны в пределах Market. Глобальные компоненты в пути запроса — публичный gateway, Dex
и SpiceDB; control plane в нём не участвует, но получает право писать маршруты публичного gateway.
Перенос Market не реализован: до него Market навсегда остаётся в Cell, где создан. После переноса
Previous Placement не освобождается никогда: данные Market лежат в нескольких Cell, UI собирает
историю заказов из всех размещений, и каждый следующий перенос добавляет ещё одно. Глобальная
аналитика по всем Market — отдельное решение.

## References

- [WSO2 — Cell-Based Architecture: A Decentralized Reference Architecture for Cloud Native Applications](https://github.com/wso2/reference-architecture/blob/master/reference-architecture-cell-based.md)

  Термин «cell» у WSO2 шире нашего: там Cell — единица разбиения по bounded context и команде
  (у нас это boundary, [ADR 2](0002-define-bounded-contexts-and-implementation-languages.md)), а
  здесь Cell — полная копия data plane для набора Market. Общее, что берём:
  - gateway — единственная точка входа в Cell, точка политик и наблюдаемости: Cosmo Router для
    клиентов и internal gateway ячейки ([ADR 15](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md))
    для вызовов между контекстами;
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
  - partition key по «зерну» предметной области — Market; алгоритм — full mapping (явный маршрут на
    каждое размещение), он же даёт override конкретного Market на конкретную Cell;
  - router — тонкий слой без бизнес-логики: публичный gateway выбирает Cell по пути;
  - control plane — CP (отказывает, но не отдаёт неверного), data plane и router — AP: Envoy
    работает на последней конфигурации при недоступном control plane (static stability);
  - нет вызовов между Cell и общих хранилищ у ячеек;
  - текущий стек — «cell zero» (`cell-1`), router добавлен перед ним;
  - Cell фиксированного, проверенного нагрузкой размера; рост — новые Cell (scale-out).

  Расхождения со стороны AWS → у нас:
  - у Cell нет общих зависимостей в пути запроса → SpiceDB и Dex общие для всех Cell: их отказ
    задевает все Market. Принято осознанно: оба stateless перед своим хранилищем, масштабируются
    горизонтально и выдерживают нагрузку всех Cell;
  - слишком крупный ключ делится дальше → Market не делится, крупный город получает Cell большего
    размера;
  - перенос ключа и вторая Cell заложены с первого дня → перенос и вторая Cell только описаны в
    стратегии расширения и не проверены;
  - перенесённый ключ целиком живёт в новой Cell → агрегаты, созданные до переноса, навсегда
    остаются в прежней Cell;
  - развёртывание волнами по Cell, лимиты и load shedding на Cell, метки `cell_id` в телеметрии →
    пока не введены.

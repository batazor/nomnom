# BFF

## Назначение

BFF — отдельный сервис и единая прикладная точка входа для клиентского API BiteRoute. Сервис построен на [WunderGraph Cosmo](https://cosmo-docs.wundergraph.com/) и публикует GraphQL API через Cosmo Router. Mobile- и web-клиенты не используют внутренние gRPC/Connect API напрямую и не зависят от внутренних адресов, Protobuf-контрактов или топологии сервисов.

Сейчас используется один BFF. Отдельные BFF для mobile и web могут появиться только при устойчивом расхождении сценариев и требований. Router и plugin могут масштабироваться горизонтально; между запросами они не хранят пользовательские сессии или состояние бизнес-операций.

Выбор Cosmo зафиксирован в [локальном ADR 3](docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md), который заменяет первоначальное решение о REST/OpenAPI API.

## Текущее состояние

Проект инициализирован официальным `wgc router plugin init`:

- `wgc` закреплён как локальная dev-зависимость версии `0.133.3`;
- Cosmo Router использует локальную композицию без обязательного подключения к control plane;
- клиентский API описывается GraphQL SDL;
- резолверы реализуются Go router plugin через сгенерированный gRPC-контракт;
- generated-код и lock-файл контракта коммитятся;
- минимальный stateless-запрос `bffInfo` подтверждает сборку полного контура.

Доменные клиентские сценарии и подключения к внутренним сервисам ещё не реализованы.

## Customer API

Публичная customer-схема в [`plugins/bff/src/schema.graphql`](plugins/bff/src/schema.graphql) зафиксирована раньше resolvers (OpenSpec change [`add-customer-graphql-contract`](../../openspec/changes/add-customer-graphql-contract/)), чтобы customer UI развивался параллельно с bounded contexts на MSW-моках той же схемы.

| Операция | Назначение | Статус |
| --- | --- | --- |
| `bffInfo` | метаданные BFF | реализована |
| `restaurants(first, after)` | рестораны с cursor-пагинацией | только схема |
| `restaurant(id)` | ресторан с меню | только схема |
| `order(id)` | заказ текущего покупателя; `null` для отсутствующего и чужого | только схема |
| `customerOrders(first, after)` | история заказов покупателя | только схема |
| `placeOrder(input)` | Place Order, идемпотентно по `idempotencyKey` | только схема |
| `requestOrderCancellation(input)` | Cancellation Request, идемпотентно по `requestId` | только схема |

Операции «только схема» возвращают GraphQL error с gRPC `Unimplemented`: `BffService` встраивает `UnimplementedBffServiceServer`.

Правила контракта:

- `Order.status` — customer-представление `OrderStatus`, которое BFF вычисляет из состояния заказа и Fulfillment Facts `orders`; назначение курьера — milestone `COURIER_ASSIGNED`. Имена FSM, Temporal и gRPC-контрактов в схему не попадают.
- Бизнес-отказы mutations возвращаются в payload (`problems`, `problem`), GraphQL errors — только transport, authentication и нереализованные операции.
- Покупатель не передаётся аргументом: subject определяется по аутентифицированному запросу.
- Моменты времени — `String` в RFC 3339, `Money.amount` — десятичная строка; unions и custom scalars не используются.
- `Order.cancellation` можно заполнить только после публикации окна отмены в `orders` `GetOrder`.

## Место в архитектуре

```text
mobile / web
      │ HTTPS + GraphQL
      ▼
edge ingress/gateway
      │
      ▼
Cosmo Router
      │ generated plugin gRPC
      ▼
BFF router plugin
      │ internal gRPC
      ├────────► orders
      ├────────► restaurants
      ├────────► delivery
      └────────► payments
```

Edge завершает внешний TLS, применяет rate limiting и базовую защиту от злоупотреблений. Cosmo Router является первой прикладной точкой. BFF остаётся сервисом клиентских сценариев, а не bounded context: у него нет собственной доменной модели или хранилища доменных данных.

Платёжные webhooks обслуживаются интеграционными адаптерами `payments`. Health checks, metrics и служебные интерфейсы имеют отдельные технические маршруты и правила доступности.

## Границы ответственности

BFF проверяет клиентский GraphQL-запрос и access token, формирует пользовательский subject context, координирует внутренние gRPC-вызовы и собирает клиентское представление. Клиентские заголовки с subject, ролями/scopes или tenant не считаются доверенным источником. Во внутренний вызов subject передаётся как исходный access token либо как короткоживущая signed internal identity; сервис-владелец проверяет token/signature, issuer, audience и expiry. Обычный внутренний header сам по себе доверенным не является.

При этом:

- семантическую валидацию доменной команды выполняет сервис-владелец;
- BFF может выполнять coarse-grained authorization для endpoint и раннего отказа, но authoritative check прав на доменное действие, принадлежности resource и tenant-изоляции остаётся у bounded context;
- доменные данные получаются только через gRPC API владельцев;
- BFF не читает базы данных bounded contexts;
- BFF не публикует и не потребляет Kafka-сообщения;
- BFF не запускает и не управляет Temporal workflow напрямую;
- агрегация не создаёт общую транзакцию или атомарный снимок нескольких сервисов.

Внутренний сервис принимает subject context только после криптографической проверки выбранного механизма и отдельно авторизует действие через SpiceDB. Identity BFF, mTLS, internal gateway и NetworkPolicy являются дополнительными слоями и не заменяют проверку subject. Детали зафиксированы в [документации security](../../docs/security.md) и [ADR 18](../../docs/adr/0018-spicedb-for-authorization.md).

## API composition scenarios

Эти сценарии описывают публичную GraphQL-композицию, а не отдельный bounded context. BFF не становится владельцем вызываемых бизнес-операций.

| Scenario | Actor | Intent | Result | Делегирование |
| --- | --- | --- | --- | --- |
| **Browse Restaurants** | Клиент | Найти рестораны для выбора | Собрано клиентское представление списка ресторанов | Query `List Restaurants` в `restaurants` |
| **View Restaurant Menu** | Клиент | Посмотреть ресторан и его актуальное меню | Собрано клиентское представление ресторана и меню | Queries `Get Restaurant` и `Get Menu` в `restaurants` |
| **Place Order** | Клиент | Оформить выбранные позиции | Mutation передана `orders`, клиент получил `order_id` и начальное представление заказа | Command `Place Order` в `orders`; BFF не запускает workflow самостоятельно |
| **View Order** | Клиент | Получить текущее клиентское представление заказа | Собрано разрешённое представление заказа и заранее определённые enrichment-поля | Query `Get Order` в `orders`, при необходимости queries других владельцев |
| **List My Orders** | Клиент | Получить свою историю заказов | Возвращён разрешённый список заказов клиента | Query `List Customer Orders` в `orders` |
| **Cancel Order** | Клиент | Запросить отмену заказа | Mutation передана владельцу заказа, возвращён результат принятия запроса | Command `Request Order Cancellation` в `orders`; BFF не выполняет компенсации |
| **Courier View** | Курьер | Работать с предложениями и текущей доставкой | Собраны доступные offers и текущая доставка; команды accept/reject, Pickup и Delivery Completion делегированы владельцу | Queries и commands `delivery`; BFF не владеет assignment или доставкой |

## Структура проекта

```text
boundaries/bff/
  config.yaml                 # runtime-конфигурация Cosmo Router
  config.json                 # generated execution config
  graph.yaml                  # локальная композиция graph/plugin
  package.json                # закреплённый wgc и команды проекта
  plugins/bff/
    src/schema.graphql        # публичный GraphQL-контракт
    src/main.go               # реализация plugin RPC
    src/main_test.go          # тесты plugin
    generated/                # generated gRPC-контракт и mapping
```

По мере появления сценариев код plugin будет организован vertical slices, например `checkout`, `order-tracking` и `restaurant-catalog`. Слайсы содержат прикладную координацию и инфраструктурные адаптеры, но не собственную доменную модель.

Внутренние клиенты будут генерироваться из закреплённых BSR commits согласно [общесистемному ADR 7](../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md). Интеграция этих клиентов с plugin относится к следующему шагу.

## Требования

- Node.js LTS или новее;
- npm;
- Go 1.26 или новее;
- toolchain plugin generator (`protoc`, `protoc-gen-go`, `protoc-gen-go-grpc`), который `wgc` может установить автоматически;
- Cosmo Router binary для локального запуска.

## Команды

Установить зависимости:

```bash
npm ci
```

Сгенерировать plugin-контракт, выполнить тесты, собрать plugin и локальную композицию:

```bash
npm run check
```

Отдельные команды:

```bash
npm run generate
npm test
npm run build
npm run compose
```

Скачать Router и запустить его:

```bash
npm run router:download
npm start
```

После запуска GraphQL Playground доступен на `http://localhost:3010`. Проверочный запрос:

```graphql
query BffInfo {
  bffInfo {
    name
    version
  }
}
```

## Статус контейнеризации

Выбранный runtime — WunderGraph Cosmo Router с существующим Go-плагином. Воспроизводимый
образ всего приложения пока намеренно не определяется: `wgc router download-binary`
загружает последний release Router, а его версия и checksum в репозитории не закреплены.
Существующий `plugins/bff/Dockerfile` собирает только исполняемый файл плагина и не является
запускаемым образом BFF.

Перед добавлением `boundaries/bff/Dockerfile` нужно закрепить release и checksum Cosmo Router. Build
stages должны собрать плагин и `config.json`, а в непривилегированный runtime следует
перенести только router binary, plugin, `config.json` и production `config.yaml`. Токены
для удалённой композиции графа должны передаваться через BuildKit secrets, а не через
`ARG`, `ENV` или слои образа.

## Развёртывание

Базовый Helm chart и команды его проверки описаны в [ops/README.md](ops/README.md).
Установка в кластер станет возможной после определения образа BFF (см. выше).

## Следующие шаги

- определить production-схему публикации и получения execution config через Cosmo control plane;
- выбрать propagated access token или signed internal identity, настроить OIDC/JWT и ограничения операций;
- сгенерировать внутренние gRPC-клиенты из BSR;
- реализовать resolvers customer API и политику частичных результатов;
- настроить deadlines, cancellation, идемпотентность и наблюдаемость.

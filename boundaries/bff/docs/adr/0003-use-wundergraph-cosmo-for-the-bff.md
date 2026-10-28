# 3. Use WunderGraph Cosmo for the BFF

Date: 2026-10-24

## Status

Accepted

Supercedes [2. BFF as the Client Entry Point](0002-bff-as-the-client-entry-point.md)

## Context

[ADR 2](0002-bff-as-the-client-entry-point.md) определил отдельный BFF как единую клиентскую точку входа, но предполагал собственный HTTPS/JSON REST API, OpenAPI и версию `/v1`. Для реализации выбран актуальный WunderGraph Cosmo. Прежний WunderGraph BFF SDK и `create-wundergraph-app` сняты с поддержки; текущий продукт строится вокруг GraphQL Federation и Cosmo Router.

Переход на Cosmo меняет внешний транспорт и способ описания контракта, но не меняет границы доменной ответственности: BFF не становится bounded context, не владеет доменными данными и обращается к сервисам-владельцам через их внутренние gRPC API.

## Decision

### Runtime и публичный API

Использовать WunderGraph Cosmo Router как публичный runtime BFF. Клиенты обращаются к GraphQL API Router через HTTPS. Публичный контракт задаётся GraphQL SDL и эволюционирует аддитивно; устаревающие поля помечаются `@deprecated`, а несовместимые изменения требуют отдельного миграционного решения. REST/OpenAPI-контракт и версия в URL `/v1`, предусмотренные ADR 2, не реализуются.

Edge ingress/gateway завершает внешний TLS, применяет rate limiting и базовую защиту от злоупотреблений. Rate limit — local rate limit Envoy Gateway на маршрутах Market публичного gateway: у каждого Market свой счётчик в каждой реплике Envoy, сверх квоты клиент получает `429` до Router. Квота на отдельного клиента (IP, subject) требует global rate limit с Redis и не вводится. `rate_limit` Cosmo Router не используется: ему нужен свой Redis, а ключом могли бы служить только заголовки, потому что access token проверяет plugin, а не Router. Router остаётся первой прикладной точкой.

Ретраи, circuit breaker и timeouts к bounded contexts — в internal gateway ([ADR 15](../../../../docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)); `traffic_shaping` Router описывает HTTP-транспорт к сабграфам и к локальному gRPC plugin не применяется. Plugin задаёт deadline каждому вызову контекста (`BFF__INTERNAL_GATEWAY_CALL_TIMEOUT`). Клиенты называют себя заголовком `GraphQL-Client-Name` (UI — `nomnom-ui`), Router пишет его в `wg.client.name` метрик и spans. `@cost`/`@listSize` и `security.cost_control` не используются: Router выполняет только persisted operations, их форма известна заранее. Платёжные webhooks, health checks, metrics и другие служебные интерфейсы не становятся GraphQL-операциями клиентского API.

### Модель развёртывания

На начальном этапе использовать один Cosmo Router и один локальный Go router plugin `bff`. Plugin является частью сервиса BFF и реализует клиентские GraphQL-сценарии через сгенерированный Cosmo gRPC-контракт. Router и plugin могут иметь несколько реплик и не хранят между запросами пользовательские сессии или состояние бизнес-операций.

Для локальной разработки graph компонуется из `graph.yaml` командой `wgc router compose`, а Router читает generated execution config из `config.json`. Production-интеграция с Cosmo control plane, registry и выдачей Router token определяется отдельно до развёртывания.

`wgc` закрепляется в `boundaries/bff/package.json`; generated plugin Protobuf, Go-код, mapping и lock-файл коммитятся. Локальный runtime toolchain устанавливается штатным механизмом `wgc`.

### Внутренние сервисы

Router plugin вызывает `orders`, `restaurants`, `delivery` и `payments` через их gRPC API согласно [общесистемному ADR 7](../../../../docs/adr/0007-grpc-contracts-and-buf-schema-registry.md). Внутренние клиенты будут генерироваться из неизменяемых BSR commits и использоваться инфраструктурными адаптерами клиентских сценариев.

Generated gRPC-контракт между Cosmo Router и plugin является техническим контрактом runtime и не заменяет gRPC-контракты bounded contexts. Клиенты не получают прямой доступ ни к одному из этих gRPC API.

### Ответственность и доверие

Cosmo Router и plugin совместно выполняют обязанности BFF: проверяют GraphQL-запрос, аутентификацию и общие ограничения сценария, формируют доверенный пользовательский контекст, координируют gRPC-вызовы и собирают клиентский результат. Проверка JWT router-ом и передача контекста описаны в ADR 32 и ADR 41; allowlist операций — ниже.

### Allowlist операций (persisted operations)

Router в production и в kind выполняет только операции из набора persisted operations в `persisted-operations/` — по файлу `<sha256>.json` (`{version, body}`) на операцию; дальше этот набор называется manifest. Клиент отправляет не текст операции, а её SHA-256 (64 hex-символа) в `extensions.persistedQuery.sha256Hash`. Router сверяет хеш с телом из файла. Запрос без хеша блокируется (`security.block_non_persisted_operations` без `condition`: обхода по заголовку или клиенту нет), неизвестный хеш даёт `PersistedQueryNotFound`. Так публичная точка входа исполняет только операции, которые прошли ревью вместе с кодом клиента; произвольные, глубокие и тяжёлые запросы (в том числе N+1 по связанным данным) до plugin не доходят. Introspection и playground в production выключены.

Manifest собирается командой `npm run operations` в `boundaries/bff` (`scripts/persisted-operations.mjs`), генерируется, но коммитится, как `config.json`, и копируется в образ. Router читает операции из файловой системы (`storage_providers.file_system`, `persisted_operations.storage`) по хешу запроса; Cosmo Cloud и CDN не используются. Единый PQL manifest (`persisted_operations.manifest`) Router 0.363 загружает только из S3 или CDN, поэтому он не применяется. Источники:

- операции UI: codegen с `persistedDocuments` кладёт хеш в каждый документ и пишет `persisted-documents.json` (хеш → текст); UI отправляет только хеши (и dev-сервер), текст исполняют только моки;
- операции e2e kind: `ops/kind/e2e/operations/*.graphql`, тело запроса собирает `ops/kind/scripts/graphql-body.sh`;
- операции k6 (ADR 26) добавляются так же, когда появятся сценарии.

Manifest только пополняется: файлы операций, которых уже нет в источниках, остаются, поэтому BFF с новым manifest обслуживает и предыдущий релиз UI. BFF выкатывается раньше UI или вместе с ним. Устаревшие операции удаляются вручную отдельным изменением, когда их не отправляет ни один выпущенный клиент. `npm run check` падает, если для операции источника нет закоммиченного файла или файл не совпадает с хешем тела. `npm run operations` и `npm run check` валидируют каждую операцию — и из источников, и закоммиченную, в том числе устаревшую — по `client-schema.graphql` (`graphql-js`): изменение схемы, ломающее операцию ещё выпущенного клиента, падает при сборке, а не ответом Router. Удалить или переименовать такое поле можно только после ручного удаления операций, которые его используют. Cosmo Cloud с breaking change detection не используется, эта проверка её заменяет.

Локальный `config.yaml` читает те же файлы (новый файл — сразу, без перезапуска), но принимает и текст операций (`log_unknown` пишет в лог операции не из manifest), чтобы работали ручные запросы.

MCP Gateway Cosmo для ИИ-агентов не используется; если он появится, агенты получают те же операции из manifest, а не произвольный `execute_graphql`.

Семантическая валидация, бизнес-инварианты, права на доменное действие, принадлежность ресурсов и tenant-изоляция остаются в сервисе-владельце. BFF не читает чужие БД, не участвует в Kafka и не управляет Temporal workflow.

Ошибки внутренних сервисов преобразуются в безопасные GraphQL errors со стабильными публичными codes/extensions. Необработанные внутренние сообщения, stack traces, адреса и топология не раскрываются. Детальная политика ошибок, частичных GraphQL-ответов, идемпотентности mutations и cursor-based pagination определяется вместе с первыми доменными полями схемы.

### Организация кода

GraphQL SDL находится в `plugins/bff/src/schema.graphql`. Реализация plugin начинается в `plugins/bff/src`, а при появлении доменных сценариев разделяется на vertical slices по клиентским сценариям, например `checkout`, `order-tracking` и `restaurant-catalog`. Слоя `domains` у BFF нет.

## Consequences

Cosmo даёт единый GraphQL endpoint, composition, generated типобезопасный контракт Router/plugin и путь к operation governance, tracing и control plane. Локальная композиция позволяет разрабатывать и тестировать каркас без облачной учётной записи.

Отказ от REST/OpenAPI означает, что HTTP status mapping, RFC 9457 Problem Details и `/v1` из ADR 2 больше не являются публичным контрактом BFF. Клиентам потребуется GraphQL-клиент и правила обработки частичных данных и GraphQL errors. Эксплуатация включает Router и plugin, а production потребует решения о control plane, registry, токенах и политике опубликованных операций.

Инициализированный каркас содержит только технический запрос `bffInfo`. Доменные поля, внутренние gRPC-клиенты, аутентификация и production-конфигурация не реализованы.

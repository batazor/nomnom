# Индекс ADR

Карта архитектурных решений по темам. Перед новым ADR найдите здесь тему: решение, которое уточняет или меняет существующий ADR, правит его, а не создаёт новый. Новый файл без строки в индексе `make arch-check` считает ошибкой.

## Границы и структура кода

| Решение | ADR |
| --- | --- |
| Журнал решений | [1](0001-record-architecture-decisions.md) |
| Bounded contexts и языки | [2](0002-define-bounded-contexts-and-implementation-languages.md) |
| Vertical Slice Architecture | [3](0003-use-vertical-slice-architecture-as-the-top-level-architecture.md) |
| Dependency Injection | [5](0005-dependency-injection-strategy.md) |
| DDD-структура внутри слайсов | [6](0006-ddd-structure-inside-vertical-slices.md) |
| Ubiquitous Language | [24](0024-ubiquitous-language-for-domain-modeling.md) |
| Модель архитектуры LikeC4 | [44](0044-architecture-as-code-with-likec4.md) |

## Домен и транзакции

| Решение | ADR |
| --- | --- |
| Unit of Work | [20](0020-unit-of-work-for-local-transactions.md) |
| Чистые FSM-переходы | [21](0021-pure-fsm-transitions.md) |
| Specification Pattern | [22](0022-specification-pattern-for-domain-policies.md) |

## Связь между контекстами

| Решение | ADR |
| --- | --- |
| Kafka и событийная архитектура | [4](0004-use-event-driven-architecture-with-apache-kafka.md) |
| gRPC-контракты и Buf Schema Registry | [7](0007-grpc-contracts-and-buf-schema-registry.md) |
| Transactional Inbox | [11](0011-transactional-inbox-and-idempotent-event-processing.md) |
| Transactional Outbox | [13](0013-transactional-outbox-for-integration-event-publishing.md) |
| Internal gateway для синхронных вызовов | [15](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md) |
| Запрет обхода gateway через NetworkPolicy | [16](0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md) |
| Публикация Integration Events | [25](0025-integration-event-publication.md) |
| Выборочное использование Watermill CQRS, DLQ | [28](0028-selective-use-of-watermill-cqrs.md) |
| Хранение Outbox и клиенты; доставка заменена ADR 37 | [31](0031-outbox-storage-polling-relay-and-clients.md) |
| GraphQL-подписки через Cosmo Streams | [36](0036-graphql-subscriptions-via-cosmo-streams.md) |
| Доставка Outbox через Debezium CDC | [37](0037-outbox-delivery-via-debezium-cdc.md) |

## Данные

| Решение | ADR |
| --- | --- |
| PostgreSQL и MongoDB | [8](0008-storage-strategy-postgresql-and-mongodb.md) |
| Доступ к данным без ORM | [39](0039-data-access-without-orm.md) |
| Миграции при старте процесса | [42](0042-schema-migrations-at-process-startup.md) |
| Кеширование чтений в Valkey | [43](0043-application-caching-policy.md) |

## Безопасность и identity

| Решение | ADR |
| --- | --- |
| SpiceDB | [18](0018-spicedb-for-authorization.md) |
| Service identity через ServiceAccount tokens | [27](0027-service-identity-via-projected-serviceaccount-tokens.md) |
| User identity через assertions BFF | [32](0032-user-identity-via-bff-signed-assertions.md) |
| Вход через Dex и PKCE | [35](0035-browser-login-via-dex-and-pkce.md) |

## Платформа и развёртывание

| Решение | ADR |
| --- | --- |
| Multi-stage container builds | [10](0010-использовать-multi-stage-container-builds.md) |
| Helm chart на приложение | [12](0012-helm-charts-per-deployable-application.md) |
| kind как тестовое окружение | [14](0014-использовать-kind-для-тестового-окружения-kubernetes.md) |
| Cloud-native runtime | [17](0017-cloud-native-application-runtime.md) |
| Декларативный bootstrap kind | [34](0034-declarative-kind-bootstrap.md) |
| Cell-based архитектура и control plane | [41](0041-cell-based-architecture-with-control-plane.md) |

## Наблюдаемость

| Решение | ADR |
| --- | --- |
| OpenTelemetry и Grafana | [9](0009-использовать-opentelemetry-и-grafana-observability-stack.md) |
| Dashboards as code | [29](0029-grafana-dashboards-as-code.md) |
| SLO и burn rate alerts | [40](0040-service-slo-and-burn-rate-alerts.md) |

## Тестирование и CI

| Решение | ADR |
| --- | --- |
| BDD для acceptance-тестов | [19](0019-bdd-for-business-acceptance-testing.md) |
| k6 для нагрузки | [26](0026-k6-for-performance-testing.md) |
| CI описан, запускается по требованию | [30](0030-ci-is-described-but-runs-only-on-demand.md) |
| Kind E2E на Chainsaw и grpcurl | [33](0033-kind-e2e-on-kyverno-chainsaw-and-grpcurl.md) |

## Frontend

| Решение | ADR |
| --- | --- |
| Astra UI; слой компонентов заменён ADR 38 | [23](0023-astra-ui-as-the-frontend-component-foundation.md) |
| Headless UI | [38](0038-headless-ui-as-the-frontend-component-foundation.md) |

## Локальные решения контекстов

| Контекст | Решение | ADR |
| --- | --- | --- |
| `bff` | Журнал решений | [1](../../boundaries/bff/docs/adr/0001-record-architecture-decisions.md) |
| `bff` | BFF как точка входа; заменён ADR 3 | [2](../../boundaries/bff/docs/adr/0002-bff-as-the-client-entry-point.md) |
| `bff` | WunderGraph Cosmo | [3](../../boundaries/bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md) |
| `controlplane` | Журнал решений | [1](../../boundaries/controlplane/docs/adr/0001-record-architecture-decisions.md) |
| `delivery` | PostgreSQL | [1](../../boundaries/delivery/docs/adr/0001-postgresql-хранилище-delivery.md) |
| `delivery` | Temporal для назначения курьера | [2](../../boundaries/delivery/docs/adr/0002-temporal-для-назначения-курьера.md) |
| `orders` | Журнал решений | [1](../../boundaries/orders/docs/adr/0001-record-architecture-decisions.md) |
| `orders` | Temporal для оркестрации | [2](../../boundaries/orders/docs/adr/0002-use-temporal-to-orchestrate-orders.md) |
| `orders` | Watermill | [3](../../boundaries/orders/docs/adr/0003-use-watermill-for-event-integration-in-orders.md) |
| `orders` | CQRS | [4](../../boundaries/orders/docs/adr/0004-использование-cqrs-в-orders.md) |
| `orders` | Order Fulfillment | [5](../../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md) |
| `payments` | PostgreSQL | [1](../../boundaries/payments/docs/adr/0001-use-postgresql-for-payments-storage.md) |
| `payments` | Event Sourcing для Payment | [2](../../boundaries/payments/docs/adr/0002-event-sourcing-for-payment.md) |
| `restaurants` | Журнал решений | [1](../../boundaries/restaurants/docs/adr/0001-record-architecture-decisions.md) |
| `restaurants` | Коллекции MongoDB | [2](../../boundaries/restaurants/docs/adr/0002-mongodb-collections-and-document-boundaries.md) |
| `restaurants` | Кеш каталога в Valkey | [3](../../boundaries/restaurants/docs/adr/0003-catalog-read-cache-in-valkey.md) |
| `ui` | Журнал решений | [1](../../boundaries/ui/docs/adr/0001-record-architecture-decisions.md) |
| `ui` | Статика на nginx и runtime-конфиг | [2](../../boundaries/ui/docs/adr/0002-static-origin-on-nginx-with-runtime-config.md) |

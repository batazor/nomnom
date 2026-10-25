# BiteRoute

BiteRoute — сервис доставки еды, объединяющий клиентов, рестораны и курьеров.

Разработка проекта ведётся по SDD (Spec-Driven Development) с помощью OpenSpec: требования и поведение системы сначала фиксируются в спецификациях, а затем переходят в план и реализацию. Подробнее — в [документации OpenSpec](openspec/README.md).

Границы самостоятельно развиваемых приложений расположены в [`boundaries/`](boundaries/). Backend-каталоги `orders`, `restaurants`, `delivery` и `payments` обозначают bounded contexts — границы доменных моделей и ответственности. `bff` и `ui` — отдельные application boundaries без собственной backend-доменной модели.

Обязательный общий словарь, правила именования и границы Published Language зафиксированы в [Ubiquitous Language](docs/ubiquitous-language.md). Локальные индексы терминов каждого bounded context перечислены в [карте глоссариев](GLOSSARY-MAP.md).

Основные Commands, Queries, участие Temporal и Kafka, клиентские API composition scenarios и открытые продуктовые вопросы собраны в [матрице use cases](docs/use-cases.md).

## Архитектура верхнего уровня

Проект использует Vertical Slice Architecture. Внутри каждого bounded context код организуется по фичам и пользовательским или системным сценариям — vertical slices. Обработка запроса, валидация, координация и проверки одного сценария располагаются вместе.

Горизонтальные технические слои не определяют основной каркас проекта. Общие доменные модели и инварианты переиспользуются внутри своего контекста без копирования по отдельным срезам.

В bounded context `orders` применяется CQRS; решение и границы его начального варианта описаны в [локальном ADR 4](boundaries/orders/docs/adr/0004-использование-cqrs-в-orders.md).

Клиентские приложения будут обращаться к единому публичному GraphQL API сервиса BFF на WunderGraph Cosmo. Cosmo Router является первой прикладной точкой после edge ingress/gateway: совместно с router plugin он проверяет запрос и аутентификацию, формирует доверенный пользовательский контекст, координирует внутренние gRPC-вызовы и собирает клиентские представления. Доменные модели и данные остаются у соответствующих bounded contexts; BFF не получает собственную доменную модель или хранилище доменных данных.

Назначение, границы доверия, клиентские сценарии и организация BFF описаны в [`boundaries/bff/README.md`](boundaries/bff/README.md), выбор Cosmo — в [локальном ADR 3 сервиса BFF](boundaries/bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md).

Customer web-приложение находится в [`boundaries/ui/`](boundaries/ui/README.md). Его frontend conventions, feature organization, design tokens, GraphQL boundary, accessibility и open decisions описаны в [`docs/ui.md`](docs/ui.md).

## Authentication и authorization

Authentication устанавливает identity вызывающей стороны; authorization отвечает, может ли subject выполнить permission над конкретным resource. Для централизованного ReBAC выбран SpiceDB. Он хранит authorization graph, но не является identity provider и не отвечает за login, sessions или tokens.

BFF проверяет внешнюю identity и может выполнять coarse-grained проверки. Subject передаётся bounded context через проверяемый механизм — propagated access token или signed internal identity, а не через доверенный клиентский header. Каждый bounded context выполняет authoritative check перед защищённым use case, после чего доменная модель отдельно проверяет текущее состояние и инварианты. Temporal Activities, consumers и background jobs используют собственную `service` identity без impersonation пользователя.

Единая будущая schema будет храниться в `authorization/schema.zed`; на текущем шаге она не создаётся. Authorization port размещается в `applications/`, SpiceDB adapter — в `infrastructure/`, а `domains/` не зависит от SpiceDB. Полный contract, consistency, lifecycle relationships, failure policy и testing описаны в [документации security](docs/security.md), решение — в [ADR 18](docs/adr/0018-spicedb-for-authorization.md).

## Синхронное взаимодействие

Синхронные service-to-service API строятся на gRPC и Protocol Buffers. Каждый bounded context владеет своей схемой, размещает её рядом с gRPC-адаптером в `infrastructure/grpc` и публикует отдельный модуль в [Buf Schema Registry организации `nomnom`](https://buf.build/nomnom/repositories). Потребители, включая BFF, генерируют и коммитят клиентский код из закреплённого BSR commit; protobuf-типы не выходят за пределы инфраструктурных адаптеров. Клиентские приложения не используют внутренние gRPC/Connect API напрямую.

Размещение модулей, правила совместимости и генерации описаны в [документации bounded contexts](boundaries/README.md#grpc-и-protocol-buffers) и [ADR 7](docs/adr/0007-grpc-contracts-and-buf-schema-registry.md).

### Internal gateway

Все синхронные вызовы бизнес-API других приложений проходят через внутренний gateway:

| Взаимодействие | Маршрут |
| --- | --- |
| Клиентское приложение | Публичный API BFF |
| BFF вызывает bounded context | BFF → internal gateway → bounded context |
| Bounded context вызывает другой bounded context | Вызывающее приложение → internal gateway → целевое приложение |
| Temporal Activity вызывает бизнес-API другого контекста | Activity worker → internal gateway → bounded context |

```text
client ──► edge ──► BFF (Cosmo Router + plugin) ──┐
orders API / Temporal Activity worker ────────────┼──► internal gateway ──► orders | restaurants | delivery | payments
restaurants | delivery | payments ────────────────┘    (Envoy Gateway, GRPCRoute по service/method)
```

BFF отвечает за клиентский API и агрегацию, gateway — за маршрутизацию и транспортные политики, бизнес-решения — bounded contexts, оркестрация заказа — Temporal. Gateway построен на Kubernetes Gateway API и Envoy Gateway и доступен только внутри кластера (Service `internal-gateway`, ClusterIP). Приложения получают его адрес из `INTERNAL_GATEWAY_ADDR`; соответствие gRPC-сервиса и метода конкретному Kubernetes Service хранится только в реестре `GRPCRoute`. Неизвестный сервис или метод получает `UNIMPLEMENTED`. Gateway сохраняет metadata, `traceparent`, status codes, deadlines и cancellation; автоматические retries включаются только для методов с определённой политикой идемпотентности.

Правило касается синхронных вызовов бизнес-API. Инфраструктура используется через штатные endpoints: Kafka — события, Temporal Server — SDK и workers, PostgreSQL и MongoDB — доступ владельца к своей БД, SpiceDB — authorization, Alloy — telemetry. SpiceDB не маршрутизируется через business gateway. Обход gateway запрещён Kubernetes NetworkPolicy: бизнес-API принимает трафик только от Envoy Proxy gateway, а приложения имеют egress только к gateway, DNS и разрешённой инфраструктуре своего владельца. Решения — [ADR 15](docs/adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md) и [ADR 16](docs/adr/0016-запрещать-обход-internal-gateway-через-cilium-и-networkpolicy.md); реестр маршрутов, политики и перечень технических подключений — в [`ops/kind/README.md`](ops/kind/README.md#internal-gateway).

## Событийное взаимодействие

Асинхронное взаимодействие между bounded contexts строится через интеграционные события, транспортом для которых служит Apache Kafka. В Go-контекстах публикация и обработка сообщений выполняются через Watermill и адаптер `watermill-kafka`. Выбор Kafka-клиентов для остальных языков остаётся открытым.

Доставка событий имеет семантику at-least-once: publisher использует Transactional Outbox, а consumer — Transactional Inbox. Каждое событие имеет стабильный `event_id`; consumer фиксирует его в одной локальной транзакции с изменением своего состояния и при повторной доставке не выполняет бизнес-действие снова. Так достигается effectively-once эффект в локальном состоянии consumer, но не глобальная exactly-once delivery между сервисами. Pipeline, границы гарантии и обязательные fault scenarios описаны в [документации событийного взаимодействия](docs/event-driven-architecture.md), решения — в [ADR 13](docs/adr/0013-transactional-outbox-for-integration-event-publishing.md) (Outbox) и [ADR 11](docs/adr/0011-transactional-inbox-and-idempotent-event-processing.md) (Inbox).

## Observability

Целевой стек использует OpenTelemetry и Grafana Alloy для инструментирования и сбора, Tempo, Mimir, Loki и Pyroscope для специализированного хранения и Grafana для общего анализа. Потоки traces, metrics, logs и profiles, правила общего контекста и границы размещения в коде описаны в [документации observability](docs/observability.md), решение — в [ADR 9](docs/adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md).

## Cloud-native runtime

Каждое запускаемое приложение само поддерживает container-aware lifecycle: различает startup, liveness и readiness, отдаёт health на отдельном техническом порту, корректно завершается по `SIGTERM` с drain начатой работы и flush telemetry, получает конфигурацию извне и учитывает resource limits контейнера. Liveness и readiness не транслируют отказ внешних зависимостей в restart loop или полный отказ сервиса. Lifecycle принадлежит composition root; бизнес-код не зависит от Kubernetes API или Docker API. Contract описан в [документации cloud-native runtime](docs/cloud-native-runtime.md), решение — в [ADR 17](docs/adr/0017-cloud-native-application-runtime.md).

## Тестовое окружение Kubernetes

Интеграционные тесты, которым нужен Kubernetes, и E2E выполняются в kind: single-node кластер, Cilium с Hubble, Envoy Gateway; развёртывание описано в Skaffold. Unit-тесты доменной и прикладной логики от кластера не зависят. Версии закреплены в [`ops/kind/versions.env`](ops/kind/versions.env), подробности — в [`ops/kind/README.md`](ops/kind/README.md) и [ADR 14](docs/adr/0014-использовать-kind-для-тестового-окружения-kubernetes.md).

```bash
make kind-e2e      # полный неинтерактивный прогон с удалением кластера (KIND_KEEP=1 — сохранить)
make kind-up       # кластер, CNI и gateway
make kind-deploy   # Skaffold: сборка и загрузка образов, gateway, charts приложений, readiness
make kind-dev      # Skaffold dev loop
make kind-test     # проверки gateway и NetworkPolicy
make kind-down     # удаление кластера
```

## Performance testing

Load, stress, spike и soak выполняются на k6 через публичный GraphQL API (`k6 → Cosmo → internal gateway → services`) с порогами только из SDD/SLO, closed-loop нагрузкой, тестовыми identity с SpiceDB fixtures и cleanup по `test_run_id`; анализ — в Grafana stack. kind используется только для smoke, capacity — в production-like окружении. Стратегия — в [Testing Strategy](docs/testing.md#performance-testing) и [`tests/performance/README.md`](tests/performance/README.md), решение — в [ADR 26](docs/adr/0026-k6-for-performance-testing.md).

## Архитектурные решения

Общесистемные архитектурные решения хранятся в [docs/adr/](docs/adr/), а локальные решения application boundaries — в `docs/adr` соответствующего каталога внутри [`boundaries/`](boundaries/).

- Установка на macOS: `brew install adr-tools`.
- Создание решения: `adr new "Название решения"`.
- Замена предыдущего решения: `adr new -s N "Название нового решения"`, где `N` — номер заменяемого ADR.

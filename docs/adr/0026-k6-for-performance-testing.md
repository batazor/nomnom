# 26. k6 for Performance Testing

Date: 2026-10-25

## Status

Accepted

Связан с [9. Использовать OpenTelemetry и Grafana observability stack](0009-использовать-opentelemetry-и-grafana-observability-stack.md), [14. Использовать kind для тестового окружения Kubernetes](0014-использовать-kind-для-тестового-окружения-kubernetes.md) и [15. Использовать internal gateway для синхронных вызовов бизнес-API](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)

Связан с [18. SpiceDB for Authorization](0018-spicedb-for-authorization.md), [19. BDD for Business Acceptance Testing](0019-bdd-for-business-acceptance-testing.md) и [локальным ADR 3 BFF о Cosmo](../../boundaries/bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md)

Связан с [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md), [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md) и [локальным ADR 2 `orders` о Temporal](../../boundaries/orders/docs/adr/0002-use-temporal-to-orchestrate-orders.md)

## Context

[Testing Strategy](../testing.md) закрывает корректность: unit, BDD, Testcontainers и kind E2E отвечают на вопрос «работает ли поведение», но не на вопрос «как меняются latency и error rate при росте нагрузки и со временем». Цепочка nomnom — Cosmo Router, internal gateway, синхронные gRPC-вызовы, SpiceDB checks, Temporal workflows, Outbox/Inbox поверх Kafka, PostgreSQL и MongoDB — деградирует нелинейно, и часть деградации (backlog Outbox, Kafka lag, очередь Temporal task queue, утечки соединений) проявляется только под длительной нагрузкой.

Нагрузочный тест легко сделать недостоверным: обойти публичный путь и нагрузить сервис напрямую, придумать числовые пороги без требований, получать `PERMISSION_DENIED` от SpiceDB вместо бизнес-ответов, накапливать незавершённые workflows вместо steady state, мерить warm cache как cold path или запускать capacity-тест на эфемерном kind. Нужен один инструмент и правила, которые исключают эти ошибки.

На дату решения публичный GraphQL API содержит только технический `bffInfo`, BFF не развёрнут в kind, а бизнес-RPC и маршруты gateway не реализованы ([ADR 14](0014-использовать-kind-для-тестового-окружения-kubernetes.md), [ADR 15](0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)).

## Decision

### Инструмент и роль

Использовать [Grafana k6](https://grafana.com/docs/k6/latest/) как основной инструмент load и performance testing. k6 отвечает за load, stress, spike и soak и измеряет latency и error rate под нагрузкой. Он не заменяет unit, BDD, Testcontainers и kind E2E и не проверяет бизнес-правила вместо них.

Версия k6 и каждого расширения закрепляется точно, вместе с первым executable scenario, по общей политике последних стабильных релизов. Сборка с расширениями (`xk6`) допускается только с закреплёнными версиями.

### Точка входа

Клиентские сценарии проходят через реальный публичный API:

```text
k6 → GraphQL (Cosmo Router / BFF) → internal gateway → bounded contexts
```

Прямой gRPC через встроенный `k6/net/grpc` допускается только для профилирования конкретного сервиса, отдельным сценарием, с proto из закреплённого BSR commit ([ADR 7](0007-grpc-contracts-and-buf-schema-registry.md)). Его результаты не выдаются за клиентскую производительность.

Endpoint только для тестов не создаётся. Сценарий без публичной операции — заблокирован. Kitchen и courier сценарии остаются заблокированными до появления публичного API отдельных operator/courier приложений ([UI](../ui.md)).

### Closed-loop нагрузка

Заказы, созданные customer-сценарием, должны завершаться: их обрабатывают kitchen/courier сценарии или симулятор ответной стороны. Без этого workflows и очереди только растут, и soak измеряет рост backlog, а не steady state. Пока API ответной стороны нет, симулятор работает через тестовые integration events; его форма — open decision.

### Профили и thresholds

Используются четыре профиля: load (SLO при нормальной нагрузке), stress (точка и характер деградации), spike (baseline → обеденный пик → baseline) и soak (утечки, рост очередей и backlog, деградация во времени).

RPS, VU, p95/p99 и error budget не придумываются. Thresholds выводятся из performance requirement в SDD/SLO: `requirement → k6 threshold`. Пока требования нет, профиль не имеет threshold и не может считаться пройденным. Check проверяет корректность ответа, threshold — характеристику прогона; одно не заменяет другое. Прогон с нарушенным threshold завершается ошибкой; «все запросы отправлены» не является успехом.

### Identity, данные и изоляция

- Токены VU выпускает только тестовый identity issuer тестового окружения. Production credentials не используются.
- Тестовые subjects (Customer, staff Restaurant, Courier) имеют заранее созданные SpiceDB relationships в fixtures.
- Restaurant/Menu fixtures готовятся заранее; customer ID и idempotency key уникальны на логическую операцию; общий mutable Order между VU допускается только в contention-сценарии.
- Payment provider — только stub/sandbox.
- Данные прогона помечаются `test_run_id` и удаляются cleanup или TTL в PostgreSQL, MongoDB, Temporal histories и SpiceDB relationships.
- Защиты gateway: тестовые subjects в allowlist тестового окружения, либо отдельный сценарий проверяет срабатывание лимитов. `429` от защиты не считается деградацией сервиса.

### Idempotency, GraphQL и cache

Каждый `PlaceOrder` получает уникальный ключ; повтор одного ключа — отдельный сценарий. Используются реальные GraphQL-операции с operation names, каждая измеряется отдельно, размер ответа контролируется, persisted operations применяются в production-like режиме: BFF в kind и production принимает только операции своего manifest (локальный ADR 3 BFF), поэтому операции k6 лежат файлами и добавляются в manifest, как операции e2e (`ops/kind/e2e/operations`). Состояние cache (cold/warm, hit/miss) фиксируется до прогона; warm-cache результат не выдаётся за cold path.

### Request latency и бизнес-процесс

Request latency (`PlaceOrder → accepted`) отличается от business process latency (`created → delivered`). Второе измеряется business metrics, а не удержанием запроса; отдельный E2E performance сценарий может polling'ом дожидаться состояния. Kafka, Outbox/Inbox, PostgreSQL, MongoDB, Temporal и SpiceDB наблюдаются через Grafana Cloud (метрики), Tempo и Loki ([ADR 9](0009-использовать-opentelemetry-и-grafana-observability-stack.md)), а не из k6.

### Correlation и анализ

`test_run_id` передаётся в согласованном header, превращается в OTel baggage по allowlist и проходит через Cosmo и gateway, который сохраняет metadata. Он не становится metric label сервисов. Для каждого прогона сохраняются k6 summary, конфигурация, commit, environment и `test_run_id`. Stress и soak выполняются с включённым Pyroscope: k6 показывает, когда наступает деградация, profiles — почему.

### Окружения

kind — локальный smoke и проверка gateway/network path; cache там всегда холодный, окружение эфемерно, поэтому результаты дают только сравнительный сигнал, не capacity. Capacity и stress выполняются в production-like окружении: Kubernetes, gateway, реальные storage, Kafka, Temporal, observability и сопоставимые resource limits. Destructive stress против production — только по отдельному процессу. Распределённый запуск (k6-operator или одна машина) — open decision.

### Размещение и CI

Сценарии находятся в `tests/performance/`: `scenarios/` по business capability и `lib/` с GraphQL client, auth, test data, correlation и checks. Каталог не размещается внутри slices, универсальный framework не создаётся. Стратегия CI: PR — короткий smoke против kind; scheduled — load/soak/stress в production-like; manual — destructive capacity.

Полная стратегия описана в [Testing Strategy](../testing.md#performance-testing) и [`tests/performance/README.md`](../../tests/performance/README.md).

## Consequences

Performance testing получает один инструмент и воспроизводимые правила: нагрузка идёт тем же путём, что клиент, проходит authorization, завершает бизнес-процесс и оценивается по порогам из требований. Анализ остаётся в уже выбранном Grafana stack, а не в отдельной системе.

Решение требует того, чего пока нет: публичных GraphQL-операций, тестового identity issuer, SpiceDB и Restaurant/Menu fixtures, payment stub, cleanup по `test_run_id`, allowlist baggage и production-like окружения. Первые сценарии остаются заблокированными до появления этих частей. Closed loop требует поддерживать симулятор ответной стороны, пока у operator/courier нет публичного API. Без SDD/SLO требований профили дают наблюдения, но не pass/fail.

На этом шаге фиксируются решение и документация. k6 scripts, fixtures, симулятор, k6-operator и CI jobs не создаются.

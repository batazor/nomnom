# Performance tests

Load и performance testing nomnom на [k6](https://grafana.com/docs/k6/latest/). Решение — [ADR 26](../../docs/adr/0026-k6-for-performance-testing.md), место в общей стратегии — [Testing Strategy](../../docs/testing.md#performance-testing).

На текущем шаге каталог содержит только этот документ: scripts, fixtures, симулятор и pipeline не созданы.

## Роль k6

k6 измеряет latency и error rate под нагрузкой и во времени. Он не заменяет unit, BDD, Testcontainers и kind E2E: бизнес-правила, authorization policy, Inbox/Outbox guarantees и network policy проверяются там.

Версии k6 и расширений закрепляются точно вместе с первым сценарием в `tests/performance/versions.env` по политике последних стабильных релизов.

## Типы прогонов

| Профиль | Вопрос | Окружение |
| --- | --- | --- |
| Smoke | Путь `k6 → Cosmo → gateway → services` работает, checks проходят | kind |
| Load | Выполняется ли SLO при нормальной нагрузке | Production-like |
| Stress | Где точка деградации и какой у неё характер | Production-like |
| Spike | baseline → обеденный пик → baseline, возврат в норму | Production-like |
| Soak | Leaks, рост очередей, Outbox backlog, Kafka lag, backlog Temporal task queue, деградация во времени | Production-like |

Нагрузка и thresholds берутся только из performance requirement в SDD/SLO. Без требования прогон даёт наблюдение, но не pass/fail. Check — корректность ответа, threshold — характеристика прогона; нарушение threshold завершает прогон ошибкой.

## Точка входа

```text
k6 → GraphQL (Cosmo Router / BFF) → internal gateway → bounded contexts
```

Каждый запрос несёт заголовок `X-Nomnom-Market` (Market, например `berlin` в kind): по нему BFF выбирает Cell ([ADR 41](../../docs/adr/0041-cell-based-architecture-with-control-plane.md)); без него BFF отклоняет запрос. Каждый запрос — реальная операция клиентской schema с operation name; каждая операция измеряется отдельно, размер ответа контролируется, в production-like режиме используются persisted operations.

Прямой gRPC через `k6/net/grpc` с proto из закреплённого BSR commit — только для профилирования одного сервиса, в отдельном сценарии с префиксом `grpc-` и без сравнения с клиентскими результатами. Endpoint для тестов не создаётся.

## Сценарии

Сценарий доступен, когда его операции есть в публичной GraphQL schema и BFF развёрнут в целевом окружении. Сейчас schema содержит только `bffInfo`, а BFF не развёрнут в kind ([`ops/kind/README.md`](../../ops/kind/README.md)).

| Сценарий | Actor | Шаги | Статус |
| --- | --- | --- | --- |
| `browse-restaurants` | Customer | список Restaurant → Restaurant → Menu | **заблокирован**: нет публичных операций Browse Restaurants / View Restaurant Menu |
| `place-order` | Customer | browse → `PlaceOrder` mutation → `order_id`; без ожидания Delivery в запросе | **заблокирован**: нет mutation Place Order |
| `place-order-idempotency` | Customer | повтор `PlaceOrder` с одним idempotency key → тот же `order_id` | **заблокирован**: нет mutation Place Order |
| `view-order` | Customer | частые чтения активного Order | **заблокирован**: нет query View Order |
| `cancel-order` | Customer | Cancel Order в допустимых состояниях | **заблокирован**: нет mutation Cancel Order |
| `kitchen` | staff Restaurant | Kitchen Queue → Start Preparation → Mark Ready for Pickup | **заблокирован**: нет публичного API operator приложения |
| `courier` | Courier | Delivery Offer → accept → Pickup → Delivery Completion | **заблокирован**: нет публичного API courier приложения |
| `order-to-delivery` | Customer + ответная сторона | `PlaceOrder`, затем polling до Delivered — business process latency | **заблокирован**: нет customer операций и ответной стороны |

Операции и шаги взяты из [API composition scenarios](../../docs/use-cases.md#api-composition-scenarios) и [Kitchen](../../docs/kitchen.md); точные имена GraphQL-операций появятся вместе со schema. Технический `bffInfo` не является бизнес-сценарием и не используется как нагрузка.

## Closed loop

Каждый созданный Order должен дойти до конечного состояния. Kitchen и courier сценарии обрабатывают заказы customer-сценария; пока их API нет, симулятор ответной стороны публикует тестовые integration events (Acceptance, Ready for Pickup, Pickup, Delivery Completion) только для Order своего `test_run_id`. Без замкнутого цикла workflows, Kitchen Queue и Delivery Offer накапливаются, и soak измеряет рост backlog вместо steady state. Customer-сценарии без ответной стороны допустимы только в коротком smoke.

## Identity, fixtures и cleanup

- Токены VU выпускает тестовый identity issuer тестового окружения; production credentials запрещены.
- Тестовые Customer, staff Restaurant и Courier создаются fixtures вместе с SpiceDB relationships; иначе операции получают denied.
- Restaurant и Menu fixtures готовятся до прогона.
- Customer ID и idempotency key уникальны на логическую операцию. Общий mutable Order между VU — только в отдельном contention-сценарии.
- Payment provider — stub/sandbox.
- Все данные прогона помечаются `test_run_id`; после прогона cleanup или TTL удаляет их из PostgreSQL, MongoDB, Temporal histories и SpiceDB relationships.
- Тестовые subjects входят в allowlist rate limiting тестового окружения. Срабатывание лимитов проверяется отдельным сценарием; `429` от защиты не считается деградацией сервиса.

## Cache

Перед прогоном фиксируется состояние cache: cold или warm, ожидаемый hit/miss. В kind cache всегда холодный. Warm-cache результат не публикуется как cold path.

## Correlation и результаты

- `test_run_id` передаётся в согласованном header; BFF переносит его в OTel baggage по allowlist, gateway сохраняет metadata.
- `test_run_id` не является metric label сервисов. Прогон находится в Grafana по `test_run_id` в traces/logs и по временному интервалу и environment в метриках Grafana Cloud.
- Kafka, Outbox/Inbox, PostgreSQL, MongoDB, Temporal и SpiceDB анализируются в Grafana stack, не из k6.
- Stress и soak выполняются с Pyroscope: k6 показывает, когда наступает деградация, profiles — почему.
- Артефакты прогона: k6 summary, конфигурация сценария, commit, environment, `test_run_id`.

## kind и production-like

kind подходит для smoke и проверки gateway/network path. Окружение эфемерно и всегда с холодным cache, поэтому его результаты — только сравнительный сигнал между коммитами, не capacity. Capacity, stress и soak выполняются в production-like окружении с Kubernetes, gateway, реальными storage, Kafka, Temporal, observability и сопоставимыми resource limits. Destructive stress против production — только по отдельному процессу.

## Структура

```text
tests/performance/
  README.md
  versions.env       — закреплённые версии k6 и расширений
  scenarios/         — по business capability: browse-restaurants, place-order, view-order, cancel-order, kitchen, courier
  lib/               — GraphQL client, auth, test data, correlation, checks
```

Сценарий читается как пользовательская нагрузка: последовательность операций actor, checks ответов и профиль. `lib/` содержит только общие helpers; универсальный framework не создаётся. Сценарии не размещаются внутри slices.

## CI

| Триггер | Прогон | Окружение |
| --- | --- | --- |
| PR | короткий smoke | kind |
| Scheduled | load, soak, stress | production-like |
| Manual | destructive capacity | production-like |

## Open decisions

- Распределённый запуск: k6-operator в Kubernetes или одна машина (в stress она сама становится bottleneck).
- Симулятор ответной стороны: владелец, механизм тестовых integration events, удаление после появления operator/courier API.
- Имя header для `test_run_id` и ключ baggage в allowlist.
- Тестовый identity issuer и выпуск токенов VU.
- Production-like окружение: где и в каком составе.

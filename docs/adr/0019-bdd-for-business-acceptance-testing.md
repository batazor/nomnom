# 19. BDD for Business Acceptance Testing

Date: 2026-10-24

## Status

Accepted

Связан с [3. Use Vertical Slice Architecture as the Top-Level Architecture](0003-use-vertical-slice-architecture-as-the-top-level-architecture.md), [11. Transactional Inbox and Idempotent Event Processing](0011-transactional-inbox-and-idempotent-event-processing.md) и [13. Transactional Outbox for Integration Event Publishing](0013-transactional-outbox-for-integration-event-publishing.md)

Связан с [14. Использовать kind для тестового окружения Kubernetes](0014-использовать-kind-для-тестового-окружения-kubernetes.md), [18. SpiceDB for Authorization](0018-spicedb-for-authorization.md) и [локальным ADR 2 `orders` о Temporal](../../boundaries/orders/docs/adr/0002-use-temporal-to-orchestrate-orders.md)

## Context

nomnom ведёт требования и архитектурные изменения через Spec-Driven Development (SDD) в `openspec/`. Спецификации описывают требования и acceptance criteria, но сами по себе не доказывают, что значимое бизнес-поведение выполняется production-кодом. Одновременно существующая стратегия уже разделяет быстрые проверки домена и application handlers, интеграционные проверки реальной инфраструктуры и системные E2E в kind.

Если применять BDD ко всем требованиям или описывать в Gherkin транспорт, хранилище и middleware, сценарии станут дорогой копией unit/integration tests и спецификаций. Нужна ограниченная роль executable acceptance scenarios, сохраняющая Vertical Slice Architecture, отделение Temporal orchestration от домена, Inbox/Outbox guarantees и границу authorization между application use case и SpiceDB policy.

## Decision

Использовать BDD для исполняемой проверки значимого бизнес-поведения. BDD дополняет SDD, но не заменяет его:

```text
SDD → Specification → Acceptance Criteria → optional BDD scenarios → Implementation → Verification
```

Не каждое требование или acceptance criterion получает BDD scenario. Сценарии выбираются, когда бизнес-правило важно выразить и проверить языком, понятным без знания реализации.

Принять два уровня BDD:

- **slice acceptance** вызывает application handler через его interface, использует реальную доменную модель и реальное хранилище из testcontainers; остальные порты (workflow, provider, authorization, clock) заменяются моками. In-memory репозиториев нет: они дублировали бы адаптеры;
- **boundary journey** проверяет исход процесса через несколько слайсов `orders`, Temporal test environment с time skipping и test adapters.

Slice-сценарий принадлежит use-case package внутри модуля slice и размещается в `<slice>/applications/<usecase>/acceptance/<usecase>.feature`. Journey принадлежит координирующему bounded context и размещается в `<boundary>/acceptance/<journey>.feature`. Общего каталога несвязанных `.feature` не создаётся. Shared step definitions для общих `Given`-состояний располагаются на уровне boundary; специфичные шаги остаются у use case.

Gherkin пишется на английском с ubiquitous language соответствующего glossary. Сценарий описывает наблюдаемое бизнес-поведение и не знает о gRPC/HTTP method, PostgreSQL/MongoDB, Kafka partition, Inbox table, Temporal Activity или framework. Варианты одного правила выражаются `Scenario Outline`.

Каждый scenario связывается со стабильным идентификатором acceptance criterion тегом `@AC-NNN`; `@FR-NNN` может дополнительно указывать requirement. `make arch-check` проверяет, что каждый такой tag разрешается ровно в один идентификатор в `openspec/specs`.

Сценарий заменяет test того же поведения, а не дублирует его: граничные случаи и технические варианты остаются обычными tests. Шаг — тело теста: ожидания моков пишутся в нём инлайн, до вызова handler, без функций-обёрток и `.Maybe()`. Текст сценария утверждает только то, что шаг действительно проверяет. Неопределённое продуктовое правило получает `@pending` и не считается окончательным исполняемым тестом. Текст спецификации в `.feature` не копируется.

Повторная доставка проверяется в BDD как бизнес-гарантия «одно логическое событие — один логический эффект». Transactional Inbox, атомарность с business storage, Outbox recovery, конкуренция и инфраструктурные сбои проверяются integration/fault tests. BDD применим к техническому отказу только тогда, когда спецификация задаёт наблюдаемый бизнес-результат.

Для защищённого use case slice acceptance с test authorization adapter доказывает, что handler соблюдает решение authorization и не изменяет состояние после отказа. Корректность SpiceDB schema, relations, consistency и policy проверяют integration tests с реальным SpiceDB.

Целевые runners при появлении первых сценариев: `godog` для Go `orders`, Cucumber.js для TypeScript `restaurants`, `cucumber` для Rust `delivery`, `pytest-bdd` для Python `payments`; BFF выбирает runner по фактическому runtime harness. Framework не добавляется в boundary до появления принадлежащего ему executable scenario.

Полная стратегия, распределение первых кандидатов и правила CI описаны в [Testing Strategy](../testing.md).

## Consequences

Критичные acceptance criteria получают исполняемую связь между спецификацией и production behaviour, а сценарии остаются устойчивыми к смене транспорта и storage. Два уровня дают быстрый feedback для одного use case и детерминированную проверку orchestration без wall-clock ожиданий и реальных провайдеров.

Появляются требования к стабильным идентификаторам спецификаций, проверке traceability и дисциплине step definitions: они только подготавливают состояние, вызывают application interface и проверяют результат, не реализуя бизнес-алгоритм повторно. Slice acceptance требует Docker, как и `make test` в `orders`. BDD не заменяет domain/application unit tests, Testcontainers, Temporal replay/timer tests или kind E2E; часть поведения неизбежно проверяется несколькими типами тестов на разных seams.

`.feature` в `orders` исполняются `godog` под `go test`; runners других boundaries не подключены.

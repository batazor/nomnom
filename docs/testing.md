# Testing Strategy

Этот документ задаёт общесистемную стратегию тестирования BiteRoute. BDD-решение зафиксировано в [ADR 19](adr/0019-bdd-for-business-acceptance-testing.md) и дополняет существующие решения о Vertical Slice Architecture, Temporal, Inbox/Outbox, authorization и kind. Load и performance testing на k6 зафиксированы в [ADR 26](adr/0026-k6-for-performance-testing.md) и описаны в разделе [Performance Testing](#performance-testing).

## SDD и executable acceptance

Основной процесс требований и архитектурных изменений — Spec-Driven Development (SDD) в [`openspec/`](../openspec/):

```text
SDD → Specification → Acceptance Criteria → optional BDD scenarios → Implementation → Verification
```

BDD превращает выбранные acceptance criteria в исполняемые бизнес-сценарии. Он не заменяет спецификацию и не применяется автоматически к каждому требованию. Сценарий оправдан, когда правило важно для бизнеса, имеет наблюдаемый результат и полезно читается без знания реализации.

Mapper, repository SQL, Protobuf serialization, DI container, Helm rendering, middleware headers и отдельные технические функции проверяются unit/integration tests, а не BDD.

## Два уровня BDD

Уровень отвечает на вопрос о масштабе проверяемого поведения: один slice либо journey через несколько slices. Внутри slice acceptance возможны два режима исполнения — domain-only и application; это не третий уровень BDD и не меняет ownership сценария.

### Slice acceptance

```text
BDD → application handler → real domain → test ports/repositories
```

Сценарий проверяет один use case через тот же application interface, которым пользуются production adapters. Используются настоящая доменная модель и управляемые test adapters портов, репозиториев, clock и authorization. Транспорт и реальная инфраструктура не запускаются.

### Boundary journey

```text
BDD → orders application + Temporal test environment (time skipping) + test ports
```

Journey проверяет бизнес-исход процесса через несколько слайсов и orchestration. Temporal test environment управляет виртуальным временем; текст сценария говорит о business deadline, а не о срабатывании Temporal timer. Реальные Kafka, базы данных, SpiceDB и payment provider не нужны.

## Кандидаты первых сценариев

| Поведение | Уровень | Основание |
| --- | --- | --- |
| Place Order | Slice acceptance, `orders` | Один use case, инварианты `Order` и наблюдаемый результат |
| Restaurant accepts or rejects Kitchen Order | Slice acceptance, `restaurants` | Два бизнес-исхода handler и реальная доменная модель |
| Courier Assignment | Slice acceptance, `delivery` | Правила назначения внутри одного bounded context |
| Authorization-sensitive operations | Slice acceptance соответствующего слайса | Handler обязан соблюдать allow/deny test authorization port; policy/schema проверяются отдельно |
| Повторная доставка результата ресторана, доставки или платежа | Slice acceptance receiving slice | Проверяется один логический бизнес-эффект, Inbox выступает test port |
| Successful fulfillment | Boundary journey, `orders` | Общий исход нескольких slices и orchestration |
| Restaurant rejection | Boundary journey, `orders` | Business failure и последующие действия процесса |
| Restaurant confirmation timeout | Boundary journey, `orders` | Product deadline через Temporal time skipping |
| Order Cancellation на разных стадиях | Boundary journey, `orders`, `Scenario Outline` | Результат зависит от стадии и уже выполненных шагов |
| Saga compensation | Boundary journey, `orders` | Координация компенсаций нескольких bounded contexts |
| Authorization, Capture и Refund в fulfillment | Boundary journey, `orders` | Финансовые business outcomes как часть общего процесса |

Outbox recovery, атомарность `Inbox + DB transaction`, конкурентные business actions и падения инфраструктуры без специфицированного бизнес-поведения — integration/fault tests, не BDD. Полная система и Kubernetes/network policy проверяются в kind.

## Расположение и ownership

```text
<slice>/applications/<usecase>/acceptance/<usecase>.feature
<boundary>/acceptance/<journey>.feature
```

По умолчанию у use case один `.feature`, а имя файла совпадает с именем его package. `Feature:` называется по business capability (`Place order`, `Cancel order`, `Restaurant accepts order`), scenario — по конкретному правилу. Дополнительный файл допустим только когда одна самостоятельная business capability перестала читаться как единый feature; технические слои и типы тестов не являются основанием делить файл.

Владелец сценария — use-case package команды, запускающей поведение. Например, правило, проверяемое при `Request Order Cancellation`, принадлежит package этой команды внутри `applications/`, даже если само правило реализовано агрегатом `Order`. Инвариант без отдельной trigger command, например запрет вернуть завершённую Delivery в активное состояние, принадлежит bounded context и размещается в `<boundary>/acceptance/`.

`.feature` описывает доменное поведение, но физически не принадлежит domain layer. Каталоги `domains/features/`, `domains/gherkin/` и их аналоги запрещены: acceptance suite находится рядом со slice или на уровне boundary и может выбрать подходящий test seam, не меняя ownership production-кода. Глобальный каталог несвязанных `.feature` также запрещён.

### Режимы исполнения

Все три режима используют `.feature` из `acceptance/`; режим определяет только test seam и wiring step definitions, а не словарь Gherkin.

| Режим | Когда применять | Test seam |
| --- | --- | --- |
| Domain-only | Правило полностью выражается чистой доменной моделью | steps → aggregate/value object |
| Application | Нужны authorization, repository, port, транзакционная координация или use-case orchestration | steps → handler → domain → test adapters |
| Boundary journey | Нужен процесс через несколько slices и durable workflow | steps → application + Temporal test environment |

Domain-only feature всё равно располагается в `acceptance/`, а не в `domains/`. Его step definitions переиспользуют domain builders/fixtures, созданные для unit tests, и не собирают сложный aggregate вручную в каждом шаге. Если подготовка состояния начинает воспроизводить production-правила, нужен более подходящий builder или другой seam.

Shared step definitions на уровне boundary содержат общие состояния, например существующий `Order` на известной стадии. Slice-специфичные шаги остаются рядом со слайсом. Step definition является test adapter: создаёт состояние, вызывает application use case, получает observable result и выполняет assertion. Он не содержит бизнес-логики и не повторяет алгоритм домена.

## Кандидаты по slices и режимам

Таблица определяет ownership и предполагаемый test seam, но не создаёт новых продуктовых правил. Scenario становится исполняемым только после появления соответствующего acceptance criterion в SDD и стабильного `AC-*`. Строки, отмеченные как требующие уточнения, до этого могут существовать только с `@pending`.

| Кандидат | Владелец / размещение | Режим | Основание и статус SDD |
| --- | --- | --- | --- |
| Создание Order | `orders/slices/order/applications/placeorder/acceptance/placeorder.feature` | Application | Place Order читает внешние сведения через port и запускает fulfillment; конкретные acceptance criteria ещё должны быть закреплены в SDD |
| Недоступный Menu Item нельзя включить в Order | тот же `placeorder` | Application | Availability поступает через test port; само правило оформления в текущем SDD не закреплено, поэтому `@pending` до отдельного AC |
| Order Cancellation допустима или запрещена текущим состоянием | `orders/slices/order/applications/requestordercancellation/acceptance/requestordercancellation.feature` | Application | Команда является trigger и проходит authorization/handler; стадии запрета остаются открытым продуктовым вопросом, поэтому конкретные варианты — `@pending` до AC |
| Итог отмены с компенсациями до/после Order Confirmation | `orders/acceptance/<journey>.feature` | Boundary journey | Проверяет несколько slices и workflow; существующий orchestration SDD задаёт общий branching, но точные стадии и compensation policy ещё открыты |
| Невозможный переход `Order` без отдельной команды-триггера | `orders/acceptance/<invariant>.feature` | Domain-only | Boundary-level invariant; конкретный переход должен существовать в SDD, иначе `@pending` |
| Order Completion только из допустимого состояния | `orders/slices/order/applications/completeorder/acceptance/completeorder.feature` | Domain-only | Команда владеет поведением, а правило выражается агрегатом `Order`; README фиксирует completion только подтверждённого Order |
| Повторное применение одного restaurant/delivery/payment business result не создаёт второго эффекта | соответствующий use-case package `confirmorder` или `record…` | Application | Handler и test port проверяют business idempotency; `confirmorder` связан с `AC-003`, остальные результаты требуют своих AC; Inbox duplicate detection и атомарность остаются integration tests |
| Restaurant принимает Kitchen Order | `restaurants/slices/kitchenorder/applications/accept-order/acceptance/accept-order.feature` | Application | Handler загружает Restaurant, menu и Kitchen Order; домен проверяет открытость и availability |
| Restaurant отклоняет Kitchen Order | `restaurants/slices/kitchenorder/applications/reject-order/acceptance/reject-order.feature` | Domain-only | Разрешённый переход выражается состоянием Kitchen Order; причины отклонения требуют SDD, если входят в scenario |
| Kitchen Order становится ready только из допустимого состояния | `restaurants/slices/kitchenorder/applications/mark-order-ready/acceptance/mark-order-ready.feature` | Domain-only | README фиксирует переход `accepted → preparing → ready` |
| Второй Courier последовательно принимает уже назначенную Delivery | `delivery/slices/delivery/applications/accept_delivery_offer/acceptance/accept_delivery_offer.feature` | Application | Команда координирует Courier и Delivery; sequential rejection — acceptance, race двух записей — integration |
| Pickup подтверждает только назначенный Courier | `delivery/slices/delivery/applications/confirm_pickup/acceptance/confirm_pickup.feature` | Application | Actor и принадлежность проверяются на application seam; точный AC должен быть добавлен в SDD |
| Завершённая Delivery не возвращается в active без отдельной trigger command | `delivery/acceptance/<invariant>.feature` | Domain-only | README фиксирует терминальность delivered; при появлении команды перехода ownership переносится в её slice |
| Capture не превышает Authorization | `payments/slices/payment/applications/capture_payment/acceptance/capture_payment.feature` | Application | Provider — test port; текущая модель поддерживает только полный Capture, поэтому amount-варианты требуют нового AC и до него `@pending` |
| Refund не превышает Capture | `payments/slices/payment/applications/refund_payment/acceptance/refund_payment.feature` | Application | Provider — test port; partial refund сейчас открытый вопрос, поэтому amount-варианты требуют нового AC и до него `@pending` |
| Повтор Authorize/Capture/Refund не создаёт второго финансового эффекта | соответствующий `authorize_payment`, `capture_payment` или `refund_payment` | Application | Проверяется business effect через test provider port; provider callback deduplication и durable storage проверяются integration tests |

Если naming package отличается между языками, сохраняется принятая конвенция (`accept-order`, `accept_delivery_offer`, `capture_payment`); имя `.feature` буквально совпадает с package use case. Placeholder `<journey>`/`<invariant>` получает business name только после SDD, а не имя технологии или слоя.

## Язык и уровень абстракции

Gherkin пишется на английском, используя термины из [`GLOSSARY-MAP.md`](../GLOSSARY-MAP.md) и glossary соответствующего bounded context. Шаги описывают наблюдаемое бизнес-поведение:

```gherkin
Feature: Cancel order

  @AC-007
  Scenario: Customer cancels before restaurant acceptance
    Given an Order is waiting for Order Confirmation
    And its Payment is authorized
    When the customer requests Order Cancellation
    Then the Order is cancelled
    And the Authorization is released
```

Сценарий не упоминает `POST /v1/orders`, gRPC methods, PostgreSQL rows, MongoDB documents, Kafka partitions, Inbox tables, Temporal Activities или packages. Для вариантов одного правила используется `Scenario Outline`.

Также запрещены repository, SQL, Protobuf, HTTP paths/status codes, Docker, Kubernetes, DI, Unit of Work, классы и функции. `Given` не содержит raw data dump доменной структуры: используются именованные business fixtures, например `a standard order with two items`.

Business failure (`Kitchen Order` rejected, Authorization declined) выражается BDD-сценарием. Technical failure относится к integration/fault tests, если спецификация не задаёт для него отдельное наблюдаемое бизнес-поведение.

## BDD и domain unit tests

Domain unit tests исчерпывающе проверяют value objects, граничные значения, таблицы переходов и многочисленные edge cases. BDD оставляет небольшой набор ключевых правил, читаемых как спецификация, и взаимодействие нескольких правил. `Scenario Outline` используется для нескольких значимых бизнес-вариантов, а не как перенос большой edge-case matrix из unit tests. Один BDD scenario может поддерживаться несколькими более узкими unit tests.

BDD не заменяет technical integration/E2E coverage. Inbox duplicate detection и rollback, Kafka redelivery, Temporal replay, NetworkPolicy и Helm проверяются соответствующими integration, workflow или kind tests. Если технический сбой имеет специфицированный бизнес-наблюдаемый результат, `.feature` описывает только этот результат, а техническая причина отдельно воспроизводится integration test.

## Traceability

Изначально в OpenSpec-документах стабильных `FR-*` и `AC-*` идентификаторов не было; их поэтапное введение начато с criteria, выбранных для первых executable scenarios. Для новых и затрагиваемых спецификаций действует следующая схема:

- requirement получает глобально уникальный неизменяемый `FR-NNN` на отдельной строке `**ID:** FR-NNN` сразу после заголовка `### Requirement`;
- acceptance criterion, представленный OpenSpec-блоком `#### Scenario`, получает глобально уникальный неизменяемый `AC-NNN` строкой `**ID:** AC-NNN` сразу после заголовка;
- номера состоят из трёх цифр, выдаются последовательно по репозиторию, не перенумеровываются и не переиспользуются после удаления;
- существующие спецификации получают идентификаторы по мере изменения либо выбора критерия для executable scenario; BDD scenario нельзя принять без уже назначенного `AC-*`;
- каждый Gherkin scenario содержит ровно один основной tag `@AC-NNN`; `@FR-NNN` допустим как дополнительная ссылка на requirement;
- CI проверяет уникальность идентификаторов в `openspec/` и требует, чтобы каждый `@AC-*`/`@FR-*` из `.feature` ссылался ровно на один существующий ID.

`.feature` не копирует содержание спецификации. Если продуктовое правило не согласовано, scenario получает `@pending`, остаётся видимым в отчёте и исключается из набора окончательных acceptance checks до решения вопроса.

## Idempotency

BDD формулирует гарантию в бизнес-терминах: повтор одного логического события не создаёт дополнительного бизнес-эффекта. Event ID, запись Inbox и транзакционная связь Inbox с business storage относятся к integration tests согласно [Inbox ADR](adr/0011-transactional-inbox-and-idempotent-event-processing.md). Recovery publisher относится к integration/fault tests согласно [Outbox ADR](adr/0013-transactional-outbox-for-integration-event-publishing.md).

## Authorization

Slice acceptance использует test authorization port и проверяет две обязанности handler: разрешённая операция достигает доменной модели, отклонённая операция не изменяет состояние. Сценарий использует язык actor/resource/action, например «customer cannot cancel another customer's Order», и не упоминает `CheckPermission`.

Корректность SpiceDB schema, relationships, consistency, ZedToken и самой policy проверяют integration tests с реальным SpiceDB через Testcontainers согласно [ADR 18](adr/0018-spicedb-for-authorization.md). BDD доказывает enforcement решения, integration test — корректность решения policy engine.

## Пирамида и CI

| Тип | Назначение | Инструменты | Когда |
| --- | --- | --- | --- |
| Domain unit | Доменные инварианты | Native test framework языка | Каждый PR |
| Application unit | Handlers и ports | Native test framework языка | Каждый PR |
| BDD slice acceptance | Поведение use case | Gherkin + runner, test adapters | Каждый PR |
| BDD boundary journey | Outcomes orchestration | Gherkin + Temporal test environment | PR, затрагивающий boundary |
| Integration | Реальная инфраструктура и транзакционные гарантии | Testcontainers | PR, затрагивающий boundary |
| Temporal workflow tests | Replay, timers, deterministic workflow logic | Temporal test environment | PR, затрагивающий `orders` |
| System E2E | Полная система, Kubernetes и network policy | kind | Отдельный pipeline |
| Load / Performance | Latency и error rate под нагрузкой и во времени | k6 | PR smoke против kind; scheduled и manual в production-like |

BDD не заменяет unit tests, Testcontainers или kind. Тесты не используют реальные payment providers, wall-clock ожидания и shared mutable state. Обычное время управляется clock abstraction, orchestration time — Temporal time skipping.

## Runners

| Runtime | Boundary | Направление при реализации |
| --- | --- | --- |
| Go | `orders` | `godog` |
| TypeScript | `restaurants` | Cucumber.js |
| Rust | `delivery` | `cucumber` |
| Python | `payments` | `pytest-bdd` |
| BFF | `bff` | Выбрать по фактическому runtime harness |

Runner и его зависимости добавляются только вместе с первым executable scenario соответствующего boundary. Версии фиксируются на момент реализации. Отчёты runner могут публиковаться как living documentation, если генерируются из успешно выполненных сценариев и сохраняют ссылки на `AC-*`.

## Performance Testing

Решение — [ADR 26](adr/0026-k6-for-performance-testing.md); сценарии, fixtures и запуск — [`tests/performance/README.md`](../tests/performance/README.md). Load scripts, fixtures и pipeline на этом шаге не создаются.

### Роль k6

k6 отвечает за load, stress, spike и soak: как меняются latency и error rate при росте нагрузки и со временем. Корректность поведения он не доказывает и другие уровни не заменяет:

| Уровень | Инструмент |
| --- | --- |
| Unit | Native test framework языка |
| BDD / Acceptance | Gherkin + runner |
| Integration | Testcontainers |
| Kubernetes / E2E | kind |
| Load / Performance | k6 |

Версии k6 и расширений закрепляются точно вместе с первым executable scenario.

### Точка входа

Клиентская нагрузка идёт тем же путём, что клиент:

```text
k6 → GraphQL (Cosmo Router / BFF) → internal gateway → bounded contexts
```

Прямой gRPC (`k6/net/grpc`, proto из закреплённого BSR commit) допускается только для профилирования одного сервиса, отдельным сценарием, и не считается клиентской производительностью. Endpoint для тестов не создаётся: сценарий без публичной операции заблокирован. Сейчас публичная schema содержит только технический `bffInfo`, поэтому заблокированы все бизнес-сценарии, включая customer. Kitchen и courier остаются заблокированными и после появления customer-операций, пока нет публичного API отдельных operator/courier приложений ([UI](ui.md)).

### Closed loop

Заказ, созданный customer-сценарием, должен пройти Acceptance, Preparation, Pickup и Delivery Completion — через kitchen/courier сценарии или симулятор ответной стороны. Иначе workflows, Kitchen Queue и Delivery Offer только накапливаются, и soak измеряет рост backlog, а не steady state. До появления API ответной стороны симулятор публикует тестовые integration events; его владелец и механизм — open decision.

### Профили

| Профиль | Вопрос |
| --- | --- |
| Load | Выполняется ли SLO при нормальной нагрузке |
| Stress | Где точка деградации и как именно система деградирует |
| Spike | Выдерживает ли система переход baseline → обеденный пик → baseline и возвращается ли в норму |
| Soak | Есть ли memory/connection leaks, рост очередей, Outbox backlog, Kafka lag, backlog Temporal task queue, деградация во времени |

### Требования и thresholds

Числа нагрузки и пороги не придумываются: `performance requirement (SDD/SLO) → k6 threshold`. Пока требования нет, профиль даёт наблюдение без pass/fail. Check проверяет корректность каждого ответа (например, ответ содержит `order_id`), threshold — характеристику всего прогона; одно не заменяет другое. Нарушение threshold завершает прогон ошибкой, а «все запросы отправлены» успехом не является.

### Identity, данные и изоляция

- Токены VU выпускает только тестовый identity issuer тестового окружения; production credentials не используются.
- Тестовые Customer, staff Restaurant и Courier имеют заранее созданные SpiceDB relationships в fixtures — иначе authoritative check в bounded context отклонит операцию ([ADR 18](adr/0018-spicedb-for-authorization.md)).
- Restaurant/Menu fixtures готовятся до прогона. Customer ID и idempotency key уникальны на логическую операцию; общий mutable Order между VU используется только в contention-сценарии.
- Payment provider — stub/sandbox, никогда не реальный.
- Данные прогона помечаются `test_run_id` и удаляются cleanup или TTL в PostgreSQL, MongoDB, Temporal histories и SpiceDB relationships, чтобы следующий прогон не шёл на растущей базе.
- Rate limiting и защиты gateway: тестовые subjects в allowlist тестового окружения, либо отдельный сценарий проверяет срабатывание лимитов. `429` от защиты не считается деградацией сервиса.

### Idempotency

Каждый `PlaceOrder` получает уникальный idempotency key. Повтор одного ключа — отдельный сценарий, проверяющий, что повтор возвращает тот же `order_id` ([order use cases](../openspec/specs/order-use-cases/spec.md)). Обычная нагрузка не создаёт случайных дубликатов.

### GraphQL и cache

Сценарии используют реальные операции клиентской schema с operation names, без искусственных endpoint, и измеряют каждую операцию отдельно. Размер ответа контролируется. В production-like режиме используются persisted operations. Состояние cache (cold/warm, hit/miss) фиксируется до прогона; warm-cache результат не выдаётся за cold path.

### Temporal, Kafka и storage

Request latency (`PlaceOrder → accepted`) и business process latency (`created → delivered`) — разные величины. Мутация завершается после принятия команды `orders` и не ждёт доставки: BFF не управляет workflow ([BFF ADR 3](../boundaries/bff/docs/adr/0003-use-wundergraph-cosmo-for-the-bff.md)); process latency измеряется business metrics, а отдельный E2E performance сценарий может polling'ом дожидаться состояния. Kafka (lag, publish latency, Outbox backlog, Inbox duplicates, failures), PostgreSQL (pool, latency, locks, deadlocks, tx duration, CPU/IO), MongoDB (latency, pool, contention, индексы, CPU/IO) и Temporal (task queue backlog, workflow age) наблюдаются в Grafana/Mimir согласно [ADR 9](adr/0009-использовать-opentelemetry-и-grafana-observability-stack.md), а не из k6.

### Correlation и profiling

`test_run_id` передаётся в согласованном header, BFF превращает его в OTel baggage по allowlist ([observability](observability.md#distributed-tracing)), а gateway сохраняет metadata ([ADR 15](adr/0015-использовать-internal-gateway-для-синхронных-вызовов-бизнес-api.md)). `test_run_id` не становится metric label. Для каждого прогона сохраняются k6 summary, конфигурация, commit, environment и `test_run_id`. Stress и soak выполняются с Pyroscope: k6 показывает, когда наступает деградация, profiles — почему.

### Окружения

| Окружение | Назначение | Ограничение |
| --- | --- | --- |
| kind | Локальный и PR smoke, проверка gateway/network path | Cache всегда холодный, окружение эфемерно: только сравнительный сигнал, не capacity |
| Production-like | Load, stress, spike, soak, capacity | Kubernetes, gateway, реальные storage, Kafka, Temporal, observability и сопоставимые limits |
| Production | Только по отдельному процессу | Destructive stress без этого процесса запрещён |

### CI

| Триггер | Что запускается | Окружение |
| --- | --- | --- |
| PR | Короткий smoke | kind |
| Scheduled | Load, soak, stress | Production-like |
| Manual | Destructive capacity | Production-like |

Pipeline появляется вместе с первым незаблокированным сценарием.

### Open decisions

- Распределённый запуск: k6-operator в Kubernetes или одна машина. Одна машина сама становится bottleneck в stress.
- Симулятор ответной стороны: владелец, механизм публикации тестовых integration events и удаление после появления operator/courier API.
- Имя header для `test_run_id` и ключ baggage в allowlist.
- Тестовый identity issuer и способ выпуска токенов для VU.

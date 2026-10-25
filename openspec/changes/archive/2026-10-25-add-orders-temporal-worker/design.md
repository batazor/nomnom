# Design

## Context

`fulfillorder` содержит workflow, Activities, client-адаптер и функцию `Register`. Процесс `cmd/orders` — gRPC API на `pkg/lifecycle` с Wire injector `InitializeApp`; слайс `order` собирает in-memory store внутри `NewModule`. У `payments`, `restaurants` и `delivery` есть только query RPC, поэтому client-порты `Payments`, `Restaurants`, `Delivery` нечем реализовать на gRPC. Temporal в kind не развёрнут.

## Goals / Non-Goals

**Goals:**

- отдельный worker-процесс с runtime contract и Wire composition root;
- конфигурация Temporal client и `Policy` с fail-fast валидацией;
- гарантированное наличие Search Attribute до публикации stage;
- проверяемый запуск workflow end-to-end против настоящего Temporal Server.

**Non-Goals:**

- gRPC-клиенты соседних контекстов, PostgreSQL, Kafka consumers;
- старт workflow из API-процесса (command RPC `PlaceOrder` ещё не подключён);
- развёртывание Temporal Server в kind и TLS/mTLS к Temporal Cloud.

## Decisions

### Отдельный бинарник

`cmd/orders-worker` — отдельный entrypoint того же модуля и образа: API и worker масштабируются и останавливаются независимо (ADR 17). Он загружает общий `config.Config`, вызывает `di.InitializeWorker` и передаёт результат в `lifecycle.Runtime`. Бизнес-порт не открывается; management port и `/health/*` те же.

### TemporalWorker как lifecycle.Worker

| Метод | Поведение |
| --- | --- |
| `Start(ctx)` | В пределах `temporal.connect_timeout`: проверка/регистрация Search Attribute, затем `worker.Start()` (проверка namespace и начало poll). Ошибка завершает процесс non-zero |
| `Run()` | Блокируется до `Shutdown` или фатальной ошибки worker'а (`OnFatalError`) |
| `Shutdown(ctx)` | `worker.Stop()`: прекращает poll и ждёт Activities до `WorkerStopTimeout` = `shutdown.drain_timeout`; при истечении ctx возвращает ошибку |

Temporal client создаётся `client.NewLazyClient`: DI не выполняет I/O, подключение происходит в `Start` с ограниченным таймаутом. Liveness от Temporal не зависит; ready выставляется только после успешного `Start`.

### Конфигурация

```yaml
temporal:
  host_port: localhost:7233          # TEMPORAL__HOST_PORT
  namespace: default                 # TEMPORAL__NAMESPACE
  connect_timeout: 10s               # TEMPORAL__CONNECT_TIMEOUT
  register_search_attributes: false  # TEMPORAL__REGISTER_SEARCH_ATTRIBUTES
fulfillment:
  acceptance_timeout: 10m            # FULFILLMENT__ACCEPTANCE_TIMEOUT …
  assignment_timeout: 15m
  max_replans: 2
  operator_decision_timeout: 30m
  preparation_timeout: 90m
  max_preparation_extension: 60m
  pickup_timeout: 60m
  delivery_timeout: 120m
  publish_stage: true
  neighbours: unavailable            # unavailable | simulated
```

Значения по умолчанию совпадают с `fulfillorder.DefaultPolicy()`. Нулевые и отрицательные durations, отрицательный `max_replans`, пустые адрес и namespace, неизвестный режим соседей отклоняются при загрузке. Task queue не настраивается: это часть контракта ADR 5. Namespace на окружение задаётся конфигурацией.

### Search Attribute

`fulfillorder.EnsureStageSearchAttribute` при `publish_stage` читает custom Search Attributes namespace через Operator API. Если `OrderFulfillmentStage` типа Keyword есть — успех; если нет и `register_search_attributes` — `AddSearchAttributes` (AlreadyExists — успех); иначе startup завершается ошибкой с подсказкой. Атрибут с другим типом — ошибка. Регистрация — административная операция, поэтому по умолчанию выключена: в production атрибут создаёт развёртывание Temporal, локально и в dev включается флагом.

### Адаптеры соседних контекстов

`neighbours.Unavailable` возвращает retryable ошибку `ErrNotImplemented`: workflow остаётся в текущем шаге и продолжит его после выкладки worker'а с gRPC-клиентами, не теряя заказ. `neighbours.Simulated` одобряет финансовые операции и подтверждает остальные вызовы без эффекта; режим предназначен только для локальной проверки маршрута, при старте пишется предупреждение.

### Store слайса

`slices/order/di` получает провайдер `NewStore`, а `NewModule` принимает store: API и worker собирают слайс одинаково. До PostgreSQL у каждого процесса свой in-memory store, поэтому worker видит только заказы своего процесса; integration-тест засевает store напрямую.

### Развёртывание

Dockerfile собирает `/orders` и `/orders-worker` в один образ. Helm chart получает `command`, `service.enabled` и `values-worker.yaml` для отдельного release с `component: worker`, без Service и бизнес-порта.

### Проверка

Unit-тесты: конфигурация, `EnsureStageSearchAttribute` на `operatorservicemock`, lifecycle `TemporalWorker` на fake worker, адаптеры соседей. Integration-тест (build tag `integration`, `TEMPORAL_ADDRESS`) поднимает worker-компоненты против dev server, засевает `Order`, стартует workflow через `fulfillorder.Client`, отправляет факты через `Notify` и проверяет Completion и Order State `completed`. `make test-integration` запускает `temporalio/temporal:1.9.1 server start-dev` в Docker.

## Risks / Trade-offs

- [Worker с `unavailable` не продвигает workflow] → осознанно до gRPC-клиентов; заказ не теряется, Activities повторяются.
- [Отдельные in-memory store API и worker] → до PostgreSQL worker нельзя использовать в окружении с реальными заказами; README фиксирует ограничение.
- [`simulated` попадёт в окружение] → предупреждение при старте, режим не по умолчанию, values окружений его не задают.
- [Регистрация Search Attribute требует прав оператора] → по умолчанию выключена; при отсутствии атрибута startup явно падает.

## Migration Plan

Новый процесс без изменения API-процесса. Откат — удаление release worker'а; запущенные workflows ждут следующего worker'а той же версии.

## Open Questions

- Где создаётся Search Attribute в общих окружениях: Helm hook, Terraform или ручная операция — решается с развёртыванием Temporal.

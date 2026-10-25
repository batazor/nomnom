# Proposal

## Why

Temporal-адаптер [`fulfillorder`](../../../boundaries/orders/slices/order/infrastructure/temporal/fulfillorder/README.md) реализован и проверен unit-тестами, но его нечем исполнять: в `orders` есть только API-процесс, нет конфигурации Temporal client, composition root worker'а и регистрации Search Attribute `OrderFulfillmentStage`. [ADR 5](../../../boundaries/orders/docs/adr/0005-order-fulfillment-orchestration.md) и [Cloud Native Runtime](../../../docs/cloud-native-runtime.md) требуют, чтобы Temporal worker был управляемым процессом с собственным composition root.

## What Changes

- Новый entrypoint `cmd/orders-worker`: Temporal worker на task queue `orders` с runtime contract `pkg/lifecycle` (management port, readiness, SIGTERM, drain).
- Конфигурация `temporal.*` (адрес, namespace, таймаут подключения, регистрация Search Attribute) и `fulfillment.*` (значения `Policy`, `publish_stage`, режим соседних контекстов) с валидацией и переменными окружения.
- Google Wire injector `InitializeWorker` в `di`: Temporal client, `Activities`, регистрация `fulfillorder`; провайдер общего in-memory store в `slices/order/di`.
- Проверка Search Attribute `OrderFulfillmentStage` при старте и её регистрация через Operator API, если это разрешено конфигурацией.
- Адаптеры client-портов соседей `slices/order/infrastructure/neighbours`: `unavailable` (по умолчанию, retryable ошибка) и `simulated` (только для локальной проверки) — командных RPC у `payments`, `restaurants` и `delivery` пока нет.
- Сборка второго бинарника в Dockerfile, Helm values для отдельного workload `component: worker`, цели Makefile `run-worker` и `test-integration`.
- Integration-тест worker'а против Temporal dev server `temporalio/temporal:1.9.1` в Docker.

## Capabilities

### New Capabilities

- `order-fulfillment-worker`: процесс, исполняющий Order Fulfillment Workflow — конфигурация, startup и shutdown, Search Attribute, режимы адаптеров соседних контекстов.

### Modified Capabilities

Нет.

## Impact

- `boundaries/orders/cmd/orders-worker`, `di`, `pkg/config`, `slices/order/di`, `slices/order/infrastructure/neighbours`, `slices/order/infrastructure/temporal/fulfillorder` (Search Attribute).
- `Dockerfile`, `Makefile`, `config.example.yaml`, `ops/helm`, README `orders`.
- API-процесс `cmd/orders` не подключается к Temporal; gRPC-клиенты соседей, PostgreSQL и развёртывание Temporal в kind — отдельные changes.

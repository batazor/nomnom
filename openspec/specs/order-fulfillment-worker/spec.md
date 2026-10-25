# order-fulfillment-worker Specification

## Purpose
Определяет процесс `orders`, исполняющий Order Fulfillment Workflow: конфигурацию Temporal и Policy, startup и shutdown по runtime contract, Search Attribute стадии и режимы адаптеров соседних контекстов.

## Requirements

### Requirement: Worker исполняет Order Fulfillment Workflow отдельным процессом
**ID:** FR-422

`orders` SHALL иметь отдельный entrypoint `cmd/orders-worker`, который регистрирует workflow и Activities слайса `fulfillment` (`slices/fulfillment/infrastructure/temporal`) и опрашивает task queue `orders` в namespace из конфигурации. Worker MUST следовать runtime contract `pkg/lifecycle`: ready только после начала poll, при SIGTERM — not ready, readiness delay и остановка poll с ожиданием начатых Activities в пределах drain timeout.

#### Scenario: Worker исполняет заказ end-to-end
**ID:** AC-446

- **WHEN** worker запущен против Temporal Server, workflow заказа стартует через client-адаптер и получает все положительные факты
- **THEN** workflow завершается Completion, и `Order` переходит в `completed`

#### Scenario: Остановка по SIGTERM
**ID:** AC-447

- **WHEN** работающий worker получает SIGTERM
- **THEN** readiness становится not ready, worker прекращает poll и процесс завершается с кодом 0 в пределах drain timeout

### Requirement: Конфигурация Temporal и Policy проверяется при загрузке
**ID:** FR-423

Адрес и namespace Temporal, таймаут подключения, значения `Policy`, `publish_stage` и режим адаптеров соседей SHALL задаваться конфигурацией и переменными окружения. Невалидное значение MUST завершать процесс до startup.

#### Scenario: Невалидный срок
**ID:** AC-448

- **WHEN** `FULFILLMENT__ACCEPTANCE_TIMEOUT` равен `0s`
- **THEN** загрузка конфигурации возвращает ошибку, и worker не стартует

### Requirement: Search Attribute существует до публикации stage
**ID:** FR-424

При `publish_stage` worker SHALL при старте убедиться, что в namespace есть Keyword Search Attribute `OrderFulfillmentStage`. При отсутствии атрибута worker SHALL зарегистрировать его, если `register_search_attributes` включён, иначе MUST завершить startup ошибкой. При выключенном `publish_stage` проверка не выполняется.

#### Scenario: Атрибут отсутствует, регистрация разрешена
**ID:** AC-449

- **WHEN** атрибута нет и `register_search_attributes` включён
- **THEN** worker регистрирует `OrderFulfillmentStage` и продолжает startup

#### Scenario: Атрибут отсутствует, регистрация запрещена
**ID:** AC-450

- **WHEN** атрибута нет и `register_search_attributes` выключен
- **THEN** startup завершается ошибкой, называющей атрибут и способ исправления

### Requirement: Temporal недоступен — startup ограничен по времени
**ID:** FR-425

Подключение к Temporal SHALL выполняться в `Start` в пределах `temporal.connect_timeout`; DI MUST NOT выполнять сетевой I/O. Liveness MUST NOT зависеть от Temporal.

#### Scenario: Temporal Server недоступен
**ID:** AC-451

- **WHEN** Temporal Server недоступен при старте worker'а
- **THEN** процесс завершается ошибкой не позже connect timeout, не оставаясь в startup бесконечно

### Requirement: Режимы адаптеров соседних контекстов
**ID:** FR-426

Client-порты `Payments`, `Restaurants`, `Delivery` SHALL реализовываться адаптером, выбранным конфигурацией: `unavailable` по умолчанию возвращает retryable ошибку, `simulated` одобряет и подтверждает вызовы без эффекта и MUST логировать предупреждение при старте.

#### Scenario: Соседи недоступны
**ID:** AC-452

- **WHEN** worker работает в режиме `unavailable`
- **THEN** Activity вызова соседа завершается retryable ошибкой, и workflow не переходит к компенсациям

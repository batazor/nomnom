# Spec Delta

## ADDED Requirements

### Requirement: Запланированные заказы видны кухне
**ID:** FR-684

`restaurants` SHALL предоставлять query List Scheduled Orders и публиковать его по gRPC как `ListScheduledOrders(restaurant_id)`: заказы ресторана в `ACCEPTED` с окном по возрастанию `planned_ready_at`, с `restaurant_order_id`, `order_id`, позициями, окном и `planned_ready_at`. Query MUST требовать `restaurant#operate_kitchen`. Запланированные заказы MUST NOT попадать в Get Kitchen Queue до постановки в очередь.

#### Scenario: Принятый заказ ко времени
**ID:** AC-925

- **WHEN** кухня приняла заказ на 19:00 и запрашивает запланированные и очередь
- **THEN** заказ есть в List Scheduled Orders и отсутствует в Get Kitchen Queue

## MODIFIED Requirements

### Requirement: Get Kitchen Queue возвращает заказы в рекомендованном порядке
**ID:** FR-241

Get Kitchen Queue SHALL возвращать заказы ресторана в `QUEUED` с `accepted_at`, `queued_at`, `expected_ready_at`, `priority` и окном запланированного заказа в порядке правила очереди, по которому Start Next Preparation выбирает заказ. Первыми SHALL идти запланированные заказы: премиум раньше обычных, внутри — по `planned_ready_at`; затем премиум-заказы и обычные, ждущие в очереди дольше базового времени ресторана × `KITCHEN_QUEUE__AGING_FACTOR`; затем остальные обычные, внутри классов — по `queued_at`, затем по id. Порядок MUST NOT задаваться запросом; Kitchen Queue MUST NOT быть отдельным aggregate. Пустой или невалидный `KITCHEN_QUEUE__AGING_FACTOR` MUST останавливать процесс на старте.

#### Scenario: Очередь по политике
**ID:** AC-281

- **WHEN** в очереди несколько принятых заказов
- **THEN** возвращаются только заказы в `QUEUED` в порядке правила очереди

#### Scenario: Состарившийся обычный заказ
**ID:** AC-862

- **GIVEN** базовое время ресторана 20 минут, `KITCHEN_QUEUE__AGING_FACTOR = 2`, обычный заказ ждёт 45 минут, премиум-заказ поставлен позже
- **WHEN** кухня запрашивает очередь
- **THEN** обычный заказ стоит перед премиум-заказом

#### Scenario: Запланированный обгоняет всех
**ID:** AC-953

- **GIVEN** в очереди премиум-заказ и состарившийся обычный, затем поставлен запланированный обычный заказ
- **WHEN** кухня запрашивает очередь
- **THEN** запланированный заказ стоит первым

#### Scenario: Порядок запланированных
**ID:** AC-954

- **GIVEN** в очереди запланированные обычный с `planned_ready_at` 18:40 и премиум с 18:50
- **WHEN** кухня запрашивает очередь
- **THEN** премиум-заказ стоит перед обычным

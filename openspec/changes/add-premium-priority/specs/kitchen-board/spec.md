# Spec Delta

## MODIFIED Requirements

### Requirement: Get Kitchen Queue возвращает заказы в рекомендованном порядке
**ID:** FR-241

Get Kitchen Queue SHALL возвращать заказы ресторана в `QUEUED` с `accepted_at`, `queued_at`, `expected_ready_at` и `priority` в порядке правила очереди, по которому Start Next Preparation выбирает заказ. Первыми SHALL идти премиум-заказы и обычные заказы, ждущие в очереди дольше базового времени ресторана × `KITCHEN_QUEUE__AGING_FACTOR`, затем остальные обычные; внутри класса — по `queued_at`, затем по id. Порядок MUST NOT задаваться запросом; Kitchen Queue MUST NOT быть отдельным aggregate. Пустой или невалидный `KITCHEN_QUEUE__AGING_FACTOR` MUST останавливать процесс на старте.

#### Scenario: Очередь по политике
**ID:** AC-281

- **WHEN** в очереди несколько принятых заказов
- **THEN** возвращаются только заказы в `QUEUED` в порядке правила очереди

#### Scenario: Состарившийся обычный заказ
**ID:** AC-862

- **GIVEN** базовое время ресторана 20 минут, `KITCHEN_QUEUE__AGING_FACTOR = 2`, обычный заказ ждёт 45 минут, премиум-заказ поставлен позже
- **WHEN** кухня запрашивает очередь
- **THEN** обычный заказ стоит перед премиум-заказом

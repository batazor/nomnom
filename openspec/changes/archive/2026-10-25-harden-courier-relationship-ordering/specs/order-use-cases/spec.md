# Spec Delta

## ADDED Requirements

### Requirement: Конкурентный повтор Place Order возвращает зафиксированный Order
**ID:** FR-565

Если два запроса Place Order одного Customer с одним idempotency key выполняются одновременно и второй обнаруживает key, занятый закоммиченным Order первого, при сохранении key, Place Order SHALL откатить свою транзакцию, перечитать key после commit первого и вернуть его `order_id` с `replayed=true`. Если Order по key не найден, запрос MUST завершаться конфликтом конкурентности (gRPC `ABORTED`), а не внутренней ошибкой. Relationship второго запроса остаётся reconciliation.

#### Scenario: Одновременные запросы с одним ключом
**ID:** AC-671

- **WHEN** второй запрос с тем же idempotency key сохраняет key, пока первый не закоммичен, и первый затем коммитит свой Order
- **THEN** второй запрос возвращает `order_id` первого с `replayed=true` и не создаёт второй `Order`

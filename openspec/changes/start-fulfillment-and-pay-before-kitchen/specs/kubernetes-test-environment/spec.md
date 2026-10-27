# Spec Delta

## MODIFIED Requirements

### Requirement: Fulfillment-процессы orders в тестовом окружении
**ID:** FR-584

Тестовое окружение SHALL разворачивать `orders-worker` и `orders-forwarder` из chart `orders` с той же сборкой образа, что и API `orders`, с readiness и диагностикой, как у остальных процессов.

#### Scenario: Заказ начинает исполняться
**ID:** AC-739

- **WHEN** покупатель оформил заказ через BFF
- **THEN** consumer `orders` получает `OrderPlaced` и запускает workflow, workflow авторизует оплату, заказ получает статус `PAID`, и только после этого у ресторана появляется Restaurant Order в `RECEIVED`

# Proposal

## Why

Worker `orders` исполняет Order Fulfillment Workflow, но client-порты `Payments`, `Restaurants`, `Delivery` реализованы только заглушками `unavailable`/`simulated`: у `payments`, `restaurants` и `delivery` есть лишь query RPC. Без командных RPC и gRPC-клиентов workflow не может авторизовать платёж, передать заказ ресторану или создать доставку.

## What Changes

- `payments` (Python): RPC `CreatePayment`, `AuthorizePayment`, `CapturePayment`, `CancelAuthorization`, `RefundPayment` поверх существующих handlers; `PaymentOperation` с бизнес-статусом `succeeded`/`declined`/`pending`.
- `restaurants` (TypeScript): RPC `ReceiveOrderRequest`, `CancelRestaurantOrder`; поле `address` у Restaurant (регистрация и обновление), возвращаемое `GetRestaurant`.
- `delivery` (Rust): RPC `CreateDelivery`, `ReplanDelivery`, `CancelDelivery`; новый use case Cancel Delivery (State `Cancelled`, событие `DeliveryCancelled`); Replan — повторный поиск и offer курьера силами `delivery`.
- `orders` (Go): gRPC-клиенты client-портов `fulfillorder` в `slices/order/infrastructure/neighbours/grpcclient`, режим `fulfillment.neighbours: grpc` с адресом internal gateway `INTERNAL_GATEWAY_ADDR`.
- Все команды идемпотентны по `order_id` или `idempotency_key` и ключуются `order_id`, поэтому `orders` не хранит идентификаторы соседей.

## Capabilities

### New Capabilities

- `fulfillment-command-api`: синхронные командные API соседних контекстов для Order Fulfillment Workflow и их клиенты в `orders`.

### Modified Capabilities

Нет.

## Impact

- `.proto` и сгенерированный код трёх контекстов и `orders`; handlers и domain `delivery` (отмена), domain `restaurants` (адрес).
- Verified caller identity не решается: серверы сохраняют `RejectingSubjectResolver` по умолчанию (`docs/security.md`, открытый вопрос identity), поэтому в окружении вызовы отклоняются `UNAUTHENTICATED` до отдельного change; клиент считает это retryable.

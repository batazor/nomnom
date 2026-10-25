# Spec Delta

## ADDED Requirements

### Requirement: Place Order доступен через gRPC
**ID:** FR-464

`OrdersService.PlaceOrder` SHALL вызывать Place Order с Customer, равным verified subject вызова, и MUST NOT принимать customer из полей запроса. Ошибки SHALL маппиться в `google.rpc.ErrorInfo` домена `nomnom.orders.v1`.

#### Scenario: Оформление заказа клиентом
**ID:** AC-508

- **WHEN** verified user subject вызывает `PlaceOrder` с доступными позициями
- **THEN** ответ содержит `order_id`, `replayed=false`, `fulfillment_started=true`, а повтор с тем же `idempotency_key` возвращает тот же `order_id` с `replayed=true`

#### Scenario: Меню ресторана недоступно
**ID:** AC-509

- **WHEN** catalog snapshot нельзя получить, в том числе в production до API меню `restaurants`
- **THEN** `PlaceOrder` возвращает `UNAVAILABLE` с reason `DEPENDENCY_UNAVAILABLE`, и `Order` не создаётся

#### Scenario: Service subject не Customer
**ID:** AC-510

- **WHEN** `PlaceOrder` вызывает service subject
- **THEN** вызов завершается `PERMISSION_DENIED`

#### Scenario: Некорректный состав или адрес
**ID:** AC-511

- **WHEN** запрос не содержит позиций, повторяет `menu_item_id`, содержит неположительное количество или пустой Delivery Address
- **THEN** вызов завершается `INVALID_ARGUMENT` с reason `INVALID_ORDER`

### Requirement: Request Order Cancellation доступен через gRPC
**ID:** FR-465

`OrdersService.RequestOrderCancellation` SHALL вызывать Request Order Cancellation с verified subject и той же cancellation policy, что и Get Order. Недопустимое по state или окну требование и отказ workflow MUST возвращать `FAILED_PRECONDITION`/`ORDER_STATE_CONFLICT`; отсутствие workflow и недоступность SpiceDB — `UNAVAILABLE`/`DEPENDENCY_UNAVAILABLE`.

#### Scenario: Запрос передан workflow
**ID:** AC-512

- **WHEN** customer запрашивает отмену заказа в окне отмены
- **THEN** ответ содержит outcome `REQUESTED`, а для уже отменённого заказа — `ALREADY_CANCELLED`

#### Scenario: Ошибки отмены
**ID:** AC-513

- **WHEN** заказ завершён, окно закрыто, workflow отказал, workflow не найден или SpiceDB недоступен
- **THEN** вызов завершается соответственно `FAILED_PRECONDITION` (первые три случая) или `UNAVAILABLE`

### Requirement: Зависимости command RPC в API
**ID:** FR-466

Процесс API SHALL использовать lazy Temporal client, системный clock и UUIDv7 генератор `order_id`; старт и readiness MUST NOT зависеть от доступности Temporal. Тестовые адаптеры `memory.*` MUST NOT использоваться в production composition root.

#### Scenario: Temporal недоступен при старте
**ID:** AC-514

- **WHEN** API собирается с недостижимым `TEMPORAL__HOST_PORT`
- **THEN** сборка завершается успешно, а ошибку получает только вызов, которому нужен workflow

#### Scenario: Идентификатор заказа
**ID:** AC-515

- **WHEN** Place Order создаёт `Order`
- **THEN** `order_id` — UUIDv7

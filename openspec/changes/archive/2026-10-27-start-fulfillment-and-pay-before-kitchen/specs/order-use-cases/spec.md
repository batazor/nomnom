# Spec Delta

## MODIFIED Requirements

### Requirement: Place Order доступен через gRPC
**ID:** FR-464

`OrdersService.PlaceOrder` SHALL вызывать Place Order с Customer, равным verified subject вызова, и MUST NOT принимать customer из полей запроса. Ошибки SHALL маппиться в `google.rpc.ErrorInfo` домена `nomnom.orders.v1`. Ответ MUST NOT зависеть от доступности Temporal: workflow запускается consumer'ом `OrderPlaced`.

#### Scenario: Оформление заказа клиентом
**ID:** AC-508

- **WHEN** verified user subject вызывает `PlaceOrder` с доступными позициями
- **THEN** ответ содержит `order_id` и `replayed=false`, а повтор с тем же `idempotency_key` возвращает тот же `order_id` с `replayed=true`

#### Scenario: Меню ресторана недоступно
**ID:** AC-509

- **WHEN** catalog snapshot нельзя получить, в том числе в production до API меню `restaurants`
- **THEN** `PlaceOrder` возвращает `UNAVAILABLE` с reason `DEPENDENCY_UNAVAILABLE`, и `Order` не создаётся

#### Scenario: Service subject не Customer
**ID:** AC-510

- **WHEN** `PlaceOrder` вызывает service subject
- **THEN** вызов завершается `PERMISSION_DENIED`

#### Scenario: Некорректный состав
**ID:** AC-511

- **WHEN** запрос не содержит позиций, повторяет `menu_item_id` или содержит неположительное количество
- **THEN** вызов завершается `INVALID_ARGUMENT` с reason `INVALID_ORDER`

#### Scenario: Temporal недоступен при оформлении
**ID:** AC-826

- **WHEN** Temporal недоступен, а покупатель вызывает `PlaceOrder`
- **THEN** `Order` и `OrderPlaced` зафиксированы, ответ успешен, workflow стартует, когда consumer `OrderPlaced` доставит сообщение

## ADDED Requirements

### Requirement: Start Fulfillment по собственному OrderPlaced
**ID:** FR-638

Use case Start Fulfillment SHALL в одной Unit of Work записывать Inbox `(orders.startfulfillment, event_id)` и читать `Order`, а после commit — передавать порту старта `order_id`, `event_id` и момент события, в том числе при дубликате Inbox. Для `Order` в финальном состоянии use case SHALL возвращать исход `IgnoredLate` без обращения к порту. Ошибка порта SHALL возвращаться как transient.

#### Scenario: Первая доставка
**ID:** AC-827

- **WHEN** Start Fulfillment получает `OrderPlaced` заказа в `placed`
- **THEN** Inbox содержит `event_id`, порт старта вызван один раз с `order_id` и `event_id`

#### Scenario: Неизвестный Order
**ID:** AC-828

- **WHEN** `Order` с `order_id` события не найден
- **THEN** use case возвращает ошибку not found, порт старта не вызывается

### Requirement: Record Payment Authorization фиксирует оплату в Order
**ID:** FR-639

Use case Record Payment Authorization SHALL записывать Fulfillment Fact `payment_authorized` с моментом решения в `Order` в состоянии `placed`, обновлять `order_details` и Change Notification `Order` в той же транзакции. Повтор MUST быть успешным no-op без второго факта и уведомления. Для `Order` не в `placed` use case SHALL возвращать invalid transition без изменений. Integration Event MUST NOT публиковаться.

#### Scenario: Оплата записана
**ID:** AC-829

- **WHEN** worker вызывает Record Payment Authorization для `Order` в `placed`
- **THEN** у `Order` есть факт `payment_authorized`, `order_details` и Change Notification обновлены в той же транзакции

#### Scenario: Повторная запись
**ID:** AC-830

- **WHEN** факт `payment_authorized` уже записан, и вызов повторяется
- **THEN** use case завершается успешно без изменений и без нового Change Notification

## REMOVED Requirements

### Requirement: Сбой старта fulfillment не отменяет оформленный Order
**Reason**: Place Order больше не стартует workflow, поэтому сбоя старта в Place Order нет.
**Migration**: Старт выполняет Start Fulfillment по `OrderPlaced`; признак `fulfillment_started` удалён из результата и из `PlaceOrderResponse`.

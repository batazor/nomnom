# Design

## Decisions

1. Модули support делятся по ответственности, публичные имена переезжают без изменения сигнатур; потребители импортируют нужный модуль напрямую.
2. Порты доступа объявлены рядом с потребителями (ADR 6); `SpiceDbKitchenAccess` реализует три порта, `OrdersEventSourceAccessControl` — только `FulfillmentCoordinatorAccess`.
3. `CancellationDecision = cancelled | repeated | rejected(reason)` возвращает агрегат; таблица терминальных состояний переезжает в домен; внешние ответы `CancelRestaurantOrder` не меняются.
4. `requirePermission(check, subject, permission, resource)`; `publishAggregateChange(repository, outbox, aggregateType, allowlist)`.
5. `RestaurantExistence.exists(id)`, `AcceptanceFactsReader.read(restaurantId, menuItemIds) → {isOpen, available}`; реализации в infrastructure поверх тех же коллекций.

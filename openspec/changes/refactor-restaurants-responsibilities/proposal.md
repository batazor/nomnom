# Proposal

## Why

Ревью SOLID `restaurants`:

- `slices/restaurantorder/applications/restaurant-order-support.ts` — сборный модуль: представление заказа, `RepeatPolicy`, авторизация, загрузка с сокрытием чужого заказа, сохранение с публикацией и шаблон команды кухни; его импортируют 12 handlers, оба репозитория и gRPC (SRP, ISP).
- Порт `KitchenAccessControl` шире любого use case: каждый handler вызывает один метод из трёх, адаптер consumer'а заглушает два через `return false` (ISP).
- `CancelRestaurantOrderHandler` сам решает исход отмены по таблице терминальных состояний — это правило агрегата (ADR 6).
- `infrastructure/spicedb/access-control.ts` одновременно клиент SpiceDB; relationships и reconciliation импортируют клиент из модуля проверки прав.
- «Проверить право, иначе `PermissionDeniedError`» написано 5+ раз, «сохранить, забрать события, отфильтровать, добавить в Outbox» — 3 раза.
- `restaurantorder` загружает целые агрегаты `Restaurant` и `Menu`, хотя нужны факт существования, флаг открытости и доступность позиций.
- `infrastructure/grpc/subject.ts` объединяет проверку service token и user assertion.

## What Changes

- Разделить support-модуль: `restaurant-order-view.ts`, `load-restaurant-order.ts`, `publish-order-change.ts`, `kitchen-command.ts` (шаблон, `KitchenCommand`, `RepeatPolicy`).
- Узкие порты доступа: `KitchenStaffAccess`, `FulfillmentCoordinatorAccess`, `RestaurantOrderViewAccess`; SpiceDB-адаптер реализует все, адаптер consumer'а — только нужный.
- `RestaurantOrder.cancel()` возвращает решение (отменён, повтор, отказ с причиной); handler только сохраняет.
- `spicedb/client.ts` (интерфейсы, фабрика, deadline, consistency) отдельно от `access-control.ts`.
- `pkg/application/require-permission.ts` и общий `publishAggregateChange`.
- Порты `RestaurantExistence` и `AcceptanceFactsReader` вместо полных репозиториев других слайсов.
- `ServiceTokenVerifier` и `UserAssertionVerifier`, тонкий resolver.

Не входит: изменение gRPC-контрактов и поведения.

## Capabilities

### Modified Capabilities

- `restaurant-order-lifecycle`: решение об отмене принимает агрегат.
- `source-module-structure`: узкие порты и разделённые модули restaurants.

## Impact

`boundaries/restaurants` (slices, infrastructure, pkg, di, тесты, README).

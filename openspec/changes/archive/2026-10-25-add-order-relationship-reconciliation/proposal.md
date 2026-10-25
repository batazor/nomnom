# Proposal

## Why

`placeorder` пишет `order#customer` в SpiceDB до commit заказа ([security](../../../docs/security.md#dual-write-rules)). Если commit не прошёл, в graph остаётся связь без заказа; если SpiceDB потерял данные (memory datastore в kind), у существующего заказа нет связи, и клиент не видит свой заказ. Reconciliation, которого требует security, не реализован.

## What Changes

- Reconciler `order#customer`: удаляет orphan/stale relationships, восстанавливает missing (`TOUCH`) с обновлением `authorization_revision`.
- Двухфазное удаление: relationship удаляется, только если она orphan на двух проходах с интервалом не меньше grace-периода — связь оформляемого заказа существует до его commit.
- Процесс `cmd/orders-reconciler` (один экземпляр), конфигурация `RECONCILER__INTERVAL`, `RECONCILER__GRACE`, `RECONCILER__PAGE_SIZE`; log drift-счётчиков каждого прохода.
- Helm release `values-reconciler.yaml`.

## Capabilities

### New Capabilities

- `order-relationship-reconciliation`: сверка authorization graph `order#customer` с business state `orders`.

### Modified Capabilities

Нет.

## Impact

`slices/order/infrastructure/spicedb` (чтение/удаление relationships, reconciler), `infrastructure/postgres` (постраничное чтение заказов), `cmd/orders-reconciler`, `di`, Helm. Relationships других контекстов — вместе с их writers.

# Proposal

## Why

`RelationshipReconciler` в `restaurants` восстанавливает missing `restaurant#owner`/`restaurant#staff` сразу по документу ресторана из MongoDB. Revoke Staff делает `DELETE restaurant#staff` в SpiceDB до сохранения документа. Проход, прочитавший граф после `DELETE`, а документ до сохранения, видит снятого сотрудника «потерянным» и `TOUCH`-ит связь обратно: сотрудник сохраняет `operate_kitchen` и доступ к заказам ресторана, пока следующий проход не удалит её как orphan после grace. Change `harden-courier-relationship-ordering` исправил тот же риск в `orders` и `delivery` и оставил `restaurants` за рамками.

## What Changes

- **`restaurants` reconciler:** missing relationship восстанавливается по тому же правилу, что и удаление orphan: только если отсутствовала с тем же пользователем на двух проходах с интервалом не меньше grace. Кандидат, ставший present или исчезнувший из документа (revoke сохранён), забывается без записи. Счётчик `missing_pending` в log прохода, как в `delivery`.
- Почему не перечитывание под lock, как в `orders`: в MongoDB нет `FOR SHARE`, а Revoke Staff не держит lock документа от чтения до сохранения (save с optimistic version). Транзакция reconciler'а, конфликтующая с writer'ом, не закрывает окно между `DELETE` в SpiceDB и началом записи в MongoDB. Сравнение версии документа до и после чтения графа тоже не помогает: in-flight Revoke Staff ещё не изменил версию. Двухпроходное правило требует только, чтобы grace превышал длительность writer'а (`SPICEDB__TIMEOUT` и сохранение, по умолчанию grace — 10 минут), и совпадает с решением `delivery`.
- Правило применяется к `restaurant#owner` тоже: writer владельца делает только `TOUCH`, но одно правило для обеих relations проще и переживёт будущую передачу владения. Цена — восстановление потерянной связи откладывается на grace.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `restaurant-delivery-relationships`: правило восстановления missing в `restaurants` (FR-445).

## Impact

`boundaries/restaurants` (`slices/restaurant/infrastructure/reconcile`, тесты `test/reconcile.test.ts`, `test/spicedb-relationships-integration.test.ts`, `test/reconcile-mongodb-integration.test.ts`, README, README Revoke Staff), `docs/security.md`.

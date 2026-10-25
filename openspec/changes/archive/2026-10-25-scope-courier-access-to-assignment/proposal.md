# Proposal

## Why

После доставки или неудачи курьер навсегда сохраняет `view` на заказ покупателя: адрес, состав и данные покупателя остаются ему доступны.

- `delivery#courier` следует за `Delivery.courier_id` (FR-444) и удаляется только при Reassign Courier и Cancel Delivery. Confirm Delivery и Report Delivery Failure оставляют `courier_id` как исторический факт, а reconciler восстанавливает удалённую связь как missing.
- `order#view` даёт курьеру два пути: `order#courier`, который orders пишет из `CourierAssignedToDelivery`, и стрелку `delivery->act_as_courier`. Record Courier Release не реагирует на `DeliveryCompleted` и `DeliveryFailed`, поэтому `order#courier` тоже остаётся.
- Report Delivery Failure от диспетчера проверяет `delivery#act_as_courier`. Это право есть только у назначенного курьера, поэтому диспетчеру всегда отказывают.

Кто вёз заказ — факт агрегата `Delivery`. Право курьера действовать и видеть заказ — текущая возможность, она живёт, пока назначение активно. Смешение этих двух вещей нарушает принцип минимальных привилегий.

## What Changes

- **delivery**: `delivery#courier` соответствует активному назначению — `courier_id` задан и `Delivery` не delivered, не failed и не cancelled. Confirm Delivery и Report Delivery Failure удаляют связь до commit и сохраняют revision. Reconciler строит ожидаемые связи из активных назначений, поэтому закрытая доставка без связи не восстанавливается, а оставшаяся связь удаляется как stale.
- **delivery**: Report Delivery Failure от диспетчера проверяет новое право `platform#dispatch_delivery` (`operator`).
- **schema**: `order` теряет relation `courier`; `view = customer + restaurant->view_orders + delivery->act_as_courier`. Кто курьер сейчас, решает только `delivery` — владелец факта.
- **orders**: Record Courier Assignment пишет только `order#delivery`; Record Courier Release удаляет `order#delivery` только при `DeliveryCancelled`, а при снятии курьера SpiceDB не трогает. Колонки назначения и порядок по версии Delivery (FR-564) сохраняются как бизнес-данные. Reconciler не сверяет relation `courier`.
- Документация `authorization/`, `docs/security.md` и README контекстов.

Не входит: история доставок курьера. Когда появится клиент курьера, она станет read-моделью `delivery` с проверкой `courier#act` и без данных покупателя, а не доступом к `order#view`.

## Capabilities

### Modified Capabilities

- `restaurant-delivery-relationships`: `delivery#courier` следует за активным назначением.
- `spicedb-authorization`: `order#view` курьера только через `delivery`; Record Courier Assignment и Release больше не пишут `order#courier`.
- `order-relationship-reconciliation`: relation `courier` не сверяется.
- `delivery-execution`: диспетчер авторизуется правом платформы.

## Impact

- `authorization/schema.zed`, `validation.yaml`.
- `boundaries/delivery`: `applications/assignment.rs`, Confirm Delivery, Report Delivery Failure, reconcile, SpiceDB mapping.
- `boundaries/orders`: SpiceDB relationships adapter, Record Courier Assignment и Release, reconcile.
- В kind SpiceDB работает на memory datastore с bootstrap, поэтому новая schema применяется без миграции данных. Для persistent datastore перед записью schema нужно удалить связи `order#courier` (описано в design).

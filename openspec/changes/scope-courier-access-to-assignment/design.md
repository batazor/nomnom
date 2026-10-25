# Design

## Context

- В `delivery` команды курьера (Accept Offer, Confirm Pickup, Confirm Delivery, Report Failure от курьера) авторизуются `courier#act` («subject — этот курьер»); принадлежность доставки проверяет домен (`Delivery.courier_id`) и FSM. `delivery#act_as_courier` для них не нужен.
- `delivery#act_as_courier` используется стрелкой `order#view` и проверкой Report Delivery Failure от диспетчера (ошибочно).
- `orders` упорядочивает courier events по версии Delivery и хранит назначение в колонках `delivery_id`, `courier_id`, `courier_assigned_at`, `courier_delivery_version` (FR-564).

## Decisions

### 1. Право — от активного назначения, история — от агрегата

`Delivery.courier_id` остаётся после Delivered и Failed: это факт «кто вёз». Ожидаемая связь `delivery#courier@user:<courier_id>` существует, только пока назначение активно: состояние assigned или picked up. Одна функция в домене, `Delivery::active_courier()`, отвечает на этот вопрос и для writer, и для reconciler; правило не дублируется.

Confirm Delivery и Report Delivery Failure вызывают `release_assignment` с `Revoke::Relationship`: `DELETE` до commit, revision сохраняется с доставкой (FR-447). Вариант `Revoke::Keep` исчезает — отзыв выполняется при каждом окончании назначения.

Неудача `DELETE` откатывает Unit of Work (fail closed), как при Reassign и Cancel: курьер может повторить подтверждение.

### 2. Один путь к заказу — через владельца факта

Кто назначен на доставку, знает `delivery`. Копия `order#courier` в `orders` дублировала этот факт, требовала отдельного отзыва и упорядочивания событий. После изменения:

```zed
definition order {
	relation customer: user
	relation restaurant: restaurant
	relation delivery: delivery

	permission view = customer + restaurant->view_orders + delivery->act_as_courier
	permission cancel = customer
}
```

`order#delivery` связывает заказ с его доставкой: пишется Record Courier Assignment (`TOUCH`, идемпотентно; при смене доставки — `TOUCH` новой и `DELETE` прежней одним запросом) и удаляется Record Courier Release при `DeliveryCancelled`. Отзыв доступа курьера целиком на стороне `delivery`. `CourierUnassignedFromDelivery` в `orders` обновляет только колонки назначения.

Колонки назначения и порядок по версии Delivery остаются: это данные для отслеживания заказа и защита от событий не по порядку.

### 3. Диспетчер

```zed
definition platform {
	...
	/** delivery: dispatcher actions on any delivery (Report Delivery Failure). */
	permission dispatch_delivery = operator
}
```

Report Delivery Failure с reporter dispatcher проверяет `platform:nomnom#dispatch_delivery`. Остальные действия диспетчера (Reassign) уже идут через `coordinate_delivery` и не меняются.

### 4. Reconciliation

- `delivery`: ожидаемые subjects `delivery#courier` строятся из активных назначений. Связь закрытой доставки — stale и удаляется после grace; missing у закрытой доставки не восстанавливается. Правило двух проходов и защита от writer в полёте (FR-563) не меняются.
- `orders`: reconciler сверяет `customer`, `restaurant`, `delivery`; relation `courier` удалена из schema, сверять нечего.

### 5. Переход схемы

SpiceDB отказывается записать schema без relation, пока на неё есть relationships. В kind используется memory datastore с bootstrap, данные пересоздаются при смене schema. Для persistent datastore порядок: развернуть `orders` без записи `order#courier` → `zed relationship bulk-delete order courier` → записать schema.

## Risks / Trade-offs

- [`order#view` курьера теперь зависит от `delivery#courier`, который пишет другой контекст] → так и задумано: владелец факта управляет доступом; задержка между назначением и `view` прежняя — writer пишет связь до commit.
- [Курьер после доставки не видит заказ для споров и поддержки] → это работа поддержки через operator, а история курьера — будущая read-модель `delivery` без данных покупателя.

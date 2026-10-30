# Design

## Правила группы

- Группа называется частью бизнес-процесса на Ubiquitous Language, а не техническим видом кода: `offer`, но не `commands`.
- Общий код, нужный одной группе, переходит в неё. Общий код нескольких групп остаётся на верхнем уровне `applications/`.
- Сценарий без естественной группы остаётся на верхнем уровне.
- Вложенных групп нет: максимум `applications/<group>/<usecase>/`.
- Группа не содержит `domains`, `infrastructure` и `di`: они остаются на уровне слайса.
- Группа вводится, когда у неё есть бизнес-имя и она упрощает чтение. Числового порога нет.

## Раскладка

### `delivery/slices/delivery`

| Группа | Сценарии и общий код |
| --- | --- |
| `offer` | `offer_delivery`, `offer_delivery_to_courier`, `accept_delivery_offer`, `reject_delivery_offer`, `expire_delivery_offer`, `list_available_delivery_offers` |
| `assignment` | `advance_assignment`, `find_courier`, `reassign_courier`, `get_courier_pool` + общий код из нынешнего `assignment/mod.rs` |
| `execution` | `create_delivery`, `confirm_pickup`, `confirm_delivery`, `cancel_delivery`, `report_delivery_failure` |
| `tracking` | `get_order_delivery`, `get_courier_current_delivery` + общий код из нынешнего `tracking/mod.rs` |
| `duration` | `estimate_delivery_duration`, `record_delivery_duration` |
| Верхний уровень | `authorization`, `error`, `outbox`, `policy`, `relationships`, `assignment_process`, `courier_locator`, `offers_of_delivery`, `test_support` |

`assignment_process`, `courier_locator` и `offers_of_delivery` используются несколькими группами, поэтому остаются наверху.

### `orders/slices/order`

| Группа | Сценарии и общий код |
| --- | --- |
| `placement` | `placeorder`, `planschedule`, `recordpaymentauthorization`, `startfulfillment` |
| `kitchen` | `confirmorder`, `rejectorder`, `recordpreparationstarted`, `recordpreparationdelay`, `recordorderready`, `relaypreparationissue` |
| `delivery` | `recordcourierassignment`, `recordcourierrelease`, `recordpickup`, `recorddeliverycompletion`, `recorddeliveryfailure` |
| `closure` | `completeorder`, `failorcancelorder`, `requestordercancellation` |
| `lookup` | `getorder`, `getfulfillmentorder`, `listcustomerorders`, `orderdetails` |
| Верхний уровень | `errorkind`, `fulfillment`, `syncorderrelationships` |

### `restaurants/slices/restaurantorder`

| Группа | Сценарии и общий код |
| --- | --- |
| `intake` | `receive-order-request`, `accept-order`, `reject-order`, `queue-restaurant-order`, `cancel-restaurant-order` |
| `preparation` | `start-preparation`, `mark-assembled`, `mark-ready-for-pickup`, `report-preparation-delay`, `report-preparation-failure`, `record-pickup` |
| `board` | `get-kitchen-queue`, `list-active-preparations`, `list-active-restaurant-orders`, `list-incoming-orders`, `list-ready-orders`, `list-scheduled-orders`, `kitchen-board` |
| Верхний уровень | `access-control`, `get-restaurant-order`, `kitchen-command`, `load-restaurant-order`, `publish-order-change`, `restaurant-order-view` |

`kitchen-command` используют и `intake`, и `preparation`, поэтому он остаётся наверху.

## Что остаётся стабильным

- Имя последнего сегмента: Go-пакет `placeorder`, Rust-модуль `confirm_pickup`, TypeScript-модуль `accept-order`.
- Имена телеметрии: это явные строки и константы, а не пути.
- Каталог `acceptance/` с `.feature` переезжает вместе со сценарием, теги `@AC-*` не меняются.

## Механика по языкам

- **Rust.** Группа — модуль с `mod.rs`, объявляющим сценарии. Нынешний `assignment/mod.rs` и `tracking/mod.rs` становятся модулями групп, поэтому пути `applications::assignment::*` и `applications::tracking::*` сохраняются. Вспомогательная функция `identifier` из `applications/mod.rs` остаётся на месте, вызовы `super::` в сценариях получают ещё один уровень. Правило в `tests/slice_boundaries.rs` обходит каталоги рекурсивно и правки не требует.
- **Go.** Меняются import path и `dir:` в `.mockery.yml`, моки перегенерируются. Правила depguard сравнивают по префиксу и glob `**/slices/*/applications/**`, поэтому правки не требуют.
- **TypeScript.** Меняются относительные импорты; правила dependency-cruiser используют `^slices/[^/]+/applications/` и правки не требуют.

## Порядок

Применяется последним из пяти связанных changes: он меняет пути, на которые опираются остальные. Порядок: `align-use-case-readmes` → `break-restaurants-slice-cycles` → `move-restaurants-use-case-telemetry-to-slices` → `move-restaurants-grpc-to-slices` → этот change.

Первым группируется `delivery`, затем `order` и `restaurantorder`. Каждый контекст — отдельный коммит. Это чистое перемещение: логика в тех же коммитах не меняется, поэтому `git log --follow` сохраняет историю.

Пути в `openspec/changes/archive` не правятся: это история. Незакрытые задачи открытых changes касаются только выкатки и проверок, поэтому группировка им не мешает.

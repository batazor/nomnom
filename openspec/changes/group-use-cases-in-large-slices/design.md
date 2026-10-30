# Design

## Правила группы

- Группа называется частью бизнес-процесса на Ubiquitous Language, а не техническим видом кода: `offer`, но не `commands`.
- Общий код, нужный одной группе, переходит в неё. Общий код нескольких групп остаётся на верхнем уровне `applications/`.
- Сценарий без естественной группы остаётся на верхнем уровне.
- Вложенных групп нет: максимум `applications/<group>/<usecase>/`.
- Группа не содержит `domains`, `infrastructure` и `di`: они остаются на уровне слайса.
- Группа вводится, когда у неё есть бизнес-имя и она упрощает чтение. Числового порога нет.

## Раскладка

Имена групп сверены с `docs/ubiquitous-language.md` и глоссариями контекстов. Первый вариант заменён там, где имя конфликтовало с языком:

- `placement` совпадал с глобальными Market Placement и Previous Placement;
- `kitchen` и `delivery` в `orders` называли соседние контексты, хотя сами сценарии записывают Fulfillment Fact;
- `lookup` и `intake` не были терминами языка, а `lookup` к тому же называл вид кода;
- `board` давал повтор `board/kitchen-board`, хотя термин — Kitchen Board.

### `delivery/slices/delivery`

| Группа | Термин | Сценарии и общий код |
| --- | --- | --- |
| `offer` | Delivery Offer | `offer_delivery`, `offer_delivery_to_courier`, `accept_delivery_offer`, `reject_delivery_offer`, `expire_delivery_offer`, `list_available_delivery_offers` |
| `assignment` | Assignment | `create_delivery`, `advance_assignment`, `find_courier`, `reassign_courier` + общий код из `assignment/mod.rs` |
| `execution` | Delivery Execution | `confirm_pickup`, `confirm_delivery`, `cancel_delivery`, `report_delivery_failure`, `get_courier_current_delivery` |
| `tracking` | Tracking Channel | `get_order_delivery` + общий код из `tracking/mod.rs` |
| `duration` | Delivery Duration Statistics, Estimate | `estimate_delivery_duration`, `record_delivery_duration` |
| Верхний уровень | | `get_courier_pool` (Courier Pool — отдельный термин), `authorization`, `error`, `outbox`, `policy`, `relationships`, `assignment_process`, `courier_locator`, `offers_of_delivery`, `test_support` |

`create_delivery` входит в `assignment`, потому что его требование лежит в спеке `delivery-assignment`.

### `orders/slices/order`

| Группа | Термин | Сценарии |
| --- | --- | --- |
| `fulfillmentfacts` | Fulfillment Fact | `recordpaymentauthorization`, `confirmorder`, `rejectorder`, `recordpreparationstarted`, `recordorderready`, `recordcourierassignment`, `recordpickup`, `recorddeliverycompletion`, `recorddeliveryfailure` |
| `closure` | Closed Order | `completeorder`, `failorcancelorder`, `requestordercancellation` |
| Верхний уровень | | `placeorder`, `planschedule`, `startfulfillment`, `getorder`, `getfulfillmentorder`, `listcustomerorders`, `orderdetails`, `syncorderrelationships`, `recordpreparationdelay`, `relaypreparationissue`, `recordcourierrelease`, `errorkind`, `fulfillment` |

В `fulfillmentfacts` ровно девять сценариев — по одному на факт из определения. `recordpreparationdelay`, `relaypreparationissue` и `recordcourierrelease` не пишут запись в журнал Order, поэтому определение не расширяется, а они остаются на верхнем уровне.

### `restaurants/slices/restaurantorder`

| Группа | Термин | Сценарии и общий код |
| --- | --- | --- |
| `acceptance` | Acceptance, Rejection | `receive-order-request`, `accept-order`, `reject-order` |
| `preparation` | Preparation | `queue-restaurant-order`, `start-preparation`, `mark-assembled`, `mark-ready-for-pickup`, `report-preparation-delay`, `report-preparation-failure` |
| `kitchen-board` | Kitchen Board | `get-kitchen-queue`, `list-active-preparations`, `list-active-restaurant-orders`, `list-incoming-orders`, `list-ready-orders`, `list-scheduled-orders` + общий код в `kitchen-board.ts` |
| Верхний уровень | | `cancel-restaurant-order` (отмена возможна на всех стадиях Open Restaurant Order), `record-pickup` (Pickup — термин `delivery`, шаг после готовности), `get-restaurant-order`, `access-control`, `kitchen-command`, `load-restaurant-order`, `publish-order-change`, `restaurant-order-view` |

`kitchen-command` используют и `acceptance`, и `preparation`, поэтому он остаётся наверху.

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

Первым группируется `delivery`, затем `order` и `restaurantorder`. Каждый контекст — отдельный коммит. Сверка с глоссариями выполнена отдельным перемещением поверх первого варианта. Это чистое перемещение: логика в тех же коммитах не меняется, поэтому `git log --follow` сохраняет историю.

Пути в `openspec/changes/archive` не правятся: это история. Незакрытые задачи открытых changes касаются только выкатки и проверок, поэтому группировка им не мешает.

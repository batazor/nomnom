# Design

## Слайс `simulator`

В каждом из `orders`, `restaurants`, `delivery`: состояние `enabled` в своём хранилище (MongoDB `simulator` / Postgres `simulator_state`, нет записи — выключен), use cases `GetSimulator`/`SetSimulatorEnabled` с `platform#operate_simulator` и управляемый процессом цикл (ADR 17) с интервалом `SIMULATOR__INTERVAL`. Расписание (сущность → момент действия) — в памяти процесса; рестарт его сбрасывает, а состояние `enabled` сохраняется.

Simulator не обходит authorization: действия идут через настоящие use cases от имени участника — покупателя `SIMULATOR__CUSTOMER_ID`, владельца ресторана (`restaurant#operate_kitchen = owner + staff`), курьера оффера или доставки (`courier#act`, `delivery#act_as_courier`).

## Исходы

| Сервис | Состояние | Исход |
| --- | --- | --- |
| `orders` | раз в задержку | Place Order: случайный открытый ресторан, 1–3 доступные позиции по 1–2 шт.; 10% — Request Order Cancellation после следующей задержки, пока заказ `placed` |
| `restaurants` | `RECEIVED` | accept 90%, reject 10% (`KITCHEN_BUSY`) |
| `restaurants` | `QUEUED` | start preparation 90%, delay 10% |
| `restaurants` | `PREPARING` | assemble 92%, failure 5% (`ITEM_UNAVAILABLE`), delay 3% |
| `restaurants` | `ASSEMBLED` | ready |
| `delivery` | pending offer | accept 85%, reject 15%; задержка не позже `expires_at − INTERVAL` |
| `delivery` | `Assigned` | confirm pickup 95%, failure 5% |
| `delivery` | `PickedUp` | confirm delivery 95%, failure 5% |

Failure доставки — от имени курьера с кодом каталога без обязательного detail: `customer_unreachable`, `address_not_found`, `no_access`, `customer_refused`.

## Ограничения

Каждая реплика API запускает свой Simulator: N реплик — примерно N-кратный поток действий (в kind — одна реплика). `setSimulatorEnabled` в BFF переключает сервисы последовательно; частичный отказ оставляет часть включённой, повтор доводит до конца.

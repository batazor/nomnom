# Proposal

## Why

Плоский `applications/` стал длинным: 30 записей в слайсе `delivery`, 25 в `order`, 24 в `restaurantorder`, причём сценарии лежат вперемешку с общим кодом. Чтобы найти часть процесса (предложения курьеру, команды кухни), нужно просмотреть весь список. Деление на новые слайсы не подходит: сценарии работают с одним агрегатом, а ADR 6 создаёт новый слайс только для самостоятельной бизнес-возможности.

## What Changes

- Внутри `applications/` разрешается один дополнительный уровень: группа сценариев одной части процесса, `applications/<group>/<usecase>/`.
- Группируются три самых больших слайса:

  ```text
  delivery/slices/delivery/applications/
    offer/ assignment/ execution/ tracking/ duration/
    + общий код слайса

  orders/slices/order/applications/
    placement/ kitchen/ delivery/ closure/ lookup/
    + syncorderrelationships и общий код слайса

  restaurants/slices/restaurantorder/applications/
    intake/ preparation/ board/
    + get-restaurant-order и общий код слайса
  ```

- ADR 6 описывает уровень группы; ADR 3 фиксирует принцип: на каждом уровне действует одна схема, а глубина ограничена.

Не входит:

- группы со своими `domains`, `infrastructure` или `di`: это был бы пятый уровень архитектуры;
- порог числа сценариев и автоматическая проверка размера;
- группировка остальных слайсов: `restaurant` (18 записей) и `payment` (15) пока читаются без неё;
- изменение имён пакетов сценариев, телеметрии и поведения.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `source-module-structure`: группы сценариев в `applications/`.

## Impact

- `boundaries/delivery`: `slices/delivery/applications`, импорты в `di`, `infrastructure`, тестах.
- `boundaries/orders`: `slices/order/applications`, импорты, `.mockery.yml`, сгенерированные моки.
- `boundaries/restaurants`: `slices/restaurantorder/applications`, импорты, `test/`.
- `docs/testing.md` (пути `.feature`), `docs/security.md`, ADR 3, ADR 6, README сервисов, `boundaries/orders/docs/adr`.

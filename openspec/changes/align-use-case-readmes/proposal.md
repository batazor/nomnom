# Proposal

## Why

ADR 6 требует `README.md` в каталоге каждого use case: он фиксирует actor, намерение, результат и владельца данных. Девять каталогов с handler'ами README не имеют, поэтому при чтении `applications/` сценарий не отличить от общего кода (портов, ошибок, общих шагов). Это мешает и следующему change `group-use-cases-in-large-slices`: группировка опирается на то, что сценарии и общий код различимы.

## What Changes

- README появляется у девяти сценариев:
  - `delivery/slices/delivery/applications`: `estimate_delivery_duration`, `record_delivery_duration`, `get_order_delivery`;
  - `delivery/slices/simulator/applications`: `get_simulator`, `set_simulator_enabled`;
  - `orders/slices/order/applications`: `planschedule`, `syncorderrelationships`;
  - `orders/slices/simulator/applications`: `customersimulator`, `switchsimulator`.
- ADR 6 уточняет признак сценария: каталог с handler'ом, который вызывается извне слайса или фоновым процессом как самостоятельная операция.

Не входит:

- изменение кода сценариев;
- README для общего кода (`error`, `authorization`, `work` и подобных): ADR 6 его не требует;
- автоматическая проверка наличия README: граница «сценарий или общий код» не выражается в импортах, а проверка по имени файла или слову `Handler` дала бы ложные срабатывания.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `source-module-structure`: у каждого сценария есть README.

## Impact

- `boundaries/delivery`, `boundaries/orders`: только новые `README.md`.
- `docs/adr/0006-ddd-structure-inside-vertical-slices.md`.

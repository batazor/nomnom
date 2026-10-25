# Simulate Kitchen

## Сценарий

- **Тип:** Фоновый процесс (`PeriodicLoop` процесса `restaurants`, период `SIMULATOR__INTERVAL`).
- **Actor:** Demo Simulator от имени владельца ресторана.
- **Намерение:** Продвигать Restaurant Orders без участника, пока Simulator включён.
- **Результат:** Для заказа в `RECEIVED`/`QUEUED`/`PREPARING`/`ASSEMBLED` через случайную задержку `[SIMULATOR__MIN_DELAY, SIMULATOR__MAX_DELAY]` от момента, когда Simulator его увидел, вызывается настоящий kitchen use case: `RECEIVED` — accept 90 / reject 10; `QUEUED` — start preparation 90 / delay 10; `PREPARING` — assemble 92 / failure 5 / delay 3; `ASSEMBLED` — ready. Выключенный Simulator ничего не делает и забывает расписание (AC-764).

## Границы

- **Владелец:** `restaurants/slices/simulator`.
- **Домен:** `domains/simulator` (веса исходов).
- **Взаимодействие:** Читает ожидающие заказы всех ресторанов и владельца ресторана; subject — `user:<ownerId>`, поэтому действует обычная проверка `restaurant#operate_kitchen`. Ошибка use case (например, переход уже сделан человеком) пишется в лог, запись расписания отбрасывается, цикл продолжается. Расписание хранится только в памяти процесса.

## Acceptance

FR-605, AC-763, AC-764 change [`add-demo-simulator`](../../../../../../openspec/changes/add-demo-simulator/).

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

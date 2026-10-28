# Simulate Kitchen

## Сценарий

- **Тип:** Фоновый процесс (`PeriodicLoop` процесса `restaurants`, период `SIMULATOR__INTERVAL`).
- **Actor:** Demo Simulator от имени владельца ресторана.
- **Намерение:** Продвигать Restaurant Orders без участника, пока Simulator включён.
- **Результат:** Для заказа в `RECEIVED` через случайную задержку `[SIMULATOR__MIN_DELAY, SIMULATOR__MAX_DELAY]` от момента, когда Simulator его увидел, вызывается настоящий kitchen use case; для `QUEUED`/`PREPARING`/`ASSEMBLED` — в запланированный момент шага (см. «План готовки»). Веса: `RECEIVED` — accept 90 / reject 10; `QUEUED` — start preparation 90 / delay 10; `PREPARING` — assemble 92 / failure 5 / delay 3; `ASSEMBLED` — ready. Выключенный Simulator ничего не делает и забывает расписание (AC-764).

## План готовки (FR-609)

- Принимая заказ, Simulator заранее тянет задержки шагов `QUEUED → start`, `PREPARING → assemble`, `ASSEMBLED → ready` (каждая random в `[MIN_DELAY, MAX_DELAY]`, не меньше 1 мс) и передаёт `expected_ready_at` = момент принятия + их сумма; шаги выполняются в эти моменты.
- Задержка: новый `expected_ready_at` = max(план `READY`, текущий срок, now) + random-задержка; весь остаток плана сдвигается так, чтобы `READY` попал в новый срок. Отказ — как раньше.
- Старт приготовления (FR-618): `StartPreparation` назначает свободного повара Cook Pool; без свободного повара (`NoCookAvailable`) шаг остаётся просроченным и повторяется на следующем проходе без записи в лог. После старта оставшиеся шаги сдвигаются на опоздание старта; если `READY` стал позже `expected_ready_at`, Simulator сообщает задержку. Состав пула и линию Simulator не меняет.
- Запланированный заказ (с окном доставки) Simulator принимает без собственного срока: срок — его `planned_ready_at`, заказ остаётся в `ACCEPTED` и не планируется. В очередь его ставит `orders` в `start_at`; дальше Simulator ведёт его как заказ без плана.
- Заказ без плана (рестарт процесса или принят человеком): оставшиеся шаги планируются от текущего момента; если запланированный `READY` позже `expected_ready_at` заказа, Simulator сообщает задержку (`ReportPreparationDelay`) до него — это доменное действие, а не подстановка значения. Не удалась — план отбрасывается и строится заново на следующем проходе.

## Границы

- **Владелец:** `restaurants/slices/simulator`.
- **Домен:** `domains/simulator` (веса исходов).
- **Взаимодействие:** Читает ожидающие заказы всех ресторанов и владельца ресторана; subject — `user:<ownerId>`, поэтому действует обычная проверка `restaurant#operate_kitchen`. Ошибка use case (например, переход уже сделан человеком) пишется в лог, запись расписания отбрасывается, цикл продолжается. Расписание и планы хранятся только в памяти процесса. Market Simulator не задаёт и не выбирает: он не создаёт заказы, а ведёт существующие, и Market каждого приходит из сохранённого Restaurant Order (ADR 41; `market_id` в логе ошибок).

## Acceptance

FR-605, AC-763, AC-764 change [`add-demo-simulator`](../../../../../../openspec/changes/add-demo-simulator/); FR-609 change [`add-fulfillment-eta`](../../../../../../openspec/changes/archive/2026-10-27-add-fulfillment-eta/); FR-618, AC-786 change [`add-cook-and-courier-pools`](../../../../../../openspec/changes/add-cook-and-courier-pools/).

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

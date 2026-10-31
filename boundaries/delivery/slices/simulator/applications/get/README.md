# Get Simulator

## Сценарий

- **Тип:** Query.
- **Actor:** Оператор платформы (`platform#operate_simulator`).
- **Намерение:** Узнать, включён ли Simulator курьеров.
- **Результат:** Возвращён текущий переключатель; состояние не меняется.

## Границы

- **Владелец:** `delivery/slices/simulator`.
- **Домен:** `domains/simulator`.
- **Взаимодействие:** Читает сохранённое состояние Simulator.

## Реализация

- **Handler:** `mod.rs` — `Output` (`enabled`), `Handler::execute(subject)`.
- **Порты:** `SimulatorAuthorization`, `SimulatorStateRepository`.

## Acceptance

FR-604 change [`add-demo-simulator`](../../../../../../openspec/changes/add-demo-simulator/).

Общий каталог: [Delivery README](../../../../README.md).

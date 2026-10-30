# Set Simulator Enabled

## Сценарий

- **Тип:** Command.
- **Actor:** Оператор платформы (`platform#operate_simulator`).
- **Намерение:** Включить или выключить Simulator курьеров.
- **Результат:** Переключатель сохранён и переживает рестарт сервиса.

## Границы

- **Владелец:** `delivery/slices/simulator`.
- **Домен:** `domains/simulator`.
- **Взаимодействие:** Записывает состояние в хранилище сервиса. Фоновый цикл Simulator читает его на каждом проходе.

## Реализация

- **Handler:** `mod.rs` — `Input` (`enabled`), `Output` (`enabled`), `Handler::execute(subject, input)`.
- **Порты:** `SimulatorAuthorization`, `SimulatorStateRepository`.

## Acceptance

FR-604 change [`add-demo-simulator`](../../../../../../openspec/changes/add-demo-simulator/).

Общий каталог: [Delivery README](../../../../README.md).

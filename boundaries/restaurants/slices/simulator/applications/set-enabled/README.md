# Set Simulator Enabled

## Сценарий

- **Тип:** Command.
- **Actor:** Оператор платформы (`platform#operate_simulator`).
- **Намерение:** Включить или выключить Demo Simulator кухни в `restaurants`.
- **Результат:** Состояние сохранено в MongoDB и переживает рестарт; ответ — новое `enabled`. Не оператор — `PERMISSION_DENIED`, состояние не меняется (AC-762).

## Границы

- **Владелец:** `restaurants/slices/simulator`.
- **Домен:** `domains/simulator`.
- **Взаимодействие:** Upsert одиночного документа коллекции `simulator`; бизнес-фактов не публикует.

## Acceptance

FR-604, AC-761, AC-762 change [`add-demo-simulator`](../../../../../../openspec/changes/add-demo-simulator/).

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

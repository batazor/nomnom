# Get Simulator

## Сценарий

- **Тип:** Query.
- **Actor:** Оператор платформы (`platform#operate_simulator`).
- **Намерение:** Узнать, включён ли Demo Simulator кухни в `restaurants`.
- **Результат:** `enabled`; без сохранённого состояния — `false`. Не оператор — `PERMISSION_DENIED` (AC-762).

## Границы

- **Владелец:** `restaurants/slices/simulator`.
- **Домен:** `domains/simulator`.
- **Взаимодействие:** Читает одиночный документ коллекции `simulator`.

## Acceptance

FR-604, AC-761, AC-762 change [`add-demo-simulator`](../../../../../../openspec/changes/add-demo-simulator/).

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

# Take Cook Off Line

## Сценарий

- **Тип:** Command.
- **Actor:** Владелец или сотрудник ресторана (`restaurant#manage_cooks`).
- **Намерение:** Снять повара с линии.
- **Результат:** `Cook` вне линии; повтор — `changed = false`; повар с заказом в `PREPARING`/`ASSEMBLED` остаётся на линии с `CookBusyError`.

## Границы

- **Владелец:** `restaurants/slices/cook`.
- **Домен:** `domains/cook`.
- **Взаимодействие:** Занятость повара и очередь читаются из Restaurant Orders (`CookWorkload`); запись повара отправляет Change Notification kitchen board его ресторана (ADR 36). Integration Events не публикуются.

## Acceptance

FR-615, AC-779 change [`add-cook-and-courier-pools`](../../../../../../openspec/changes/add-cook-and-courier-pools/).

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

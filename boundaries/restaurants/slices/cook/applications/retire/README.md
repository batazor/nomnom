# Retire Cook

## Сценарий

- **Тип:** Command.
- **Actor:** Владелец или сотрудник ресторана (`restaurant#manage_cooks`).
- **Намерение:** Убрать повара из Cook Pool.
- **Результат:** `Cook` выбыл и больше не возвращается; повтор — `changed = false`; повар на линии — `CookOnLineError`. Заказы сохраняют его `cookId`.

## Границы

- **Владелец:** `restaurants/slices/cook`.
- **Домен:** `domains/cook`.
- **Взаимодействие:** Занятость повара и очередь читаются из Restaurant Orders (`CookWorkload`); запись повара отправляет Change Notification kitchen board его ресторана (ADR 36). Integration Events не публикуются.

## Acceptance

FR-615, AC-780 change [`add-cook-and-courier-pools`](../../../../../../openspec/changes/add-cook-and-courier-pools/).

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

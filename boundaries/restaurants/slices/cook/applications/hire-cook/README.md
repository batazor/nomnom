# Hire Cook

## Сценарий

- **Тип:** Command.
- **Actor:** Владелец или сотрудник ресторана (`restaurant#manage_cooks`).
- **Намерение:** Добавить повара в Cook Pool ресторана.
- **Результат:** `Cook` с именем вне линии; пустое или длиннее 64 символов имя — `InvalidCookError`, нет ресторана — `NotFoundError`.

## Границы

- **Владелец:** `restaurants/slices/cook`.
- **Домен:** `domains/cook`.
- **Взаимодействие:** Занятость повара и очередь читаются из Restaurant Orders (`CookWorkload`); запись повара отправляет Change Notification kitchen board его ресторана (ADR 36). Integration Events не публикуются.

## Acceptance

FR-614, AC-775–AC-777 change [`add-cook-and-courier-pools`](../../../../../../openspec/changes/add-cook-and-courier-pools/).

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

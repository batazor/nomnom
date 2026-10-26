# Put Cook On Line

## Сценарий

- **Тип:** Command.
- **Actor:** Владелец или сотрудник ресторана (`restaurant#manage_cooks`).
- **Намерение:** Поставить повара на линию: Start Preparation может его назначить.
- **Результат:** `Cook` на линии; повтор — `changed = false`; выбывший повар — `CookRetiredError`.

## Границы

- **Владелец:** `restaurants/slices/cook`.
- **Домен:** `domains/cook`.
- **Взаимодействие:** Занятость повара и очередь читаются из Restaurant Orders (`CookWorkload`); запись повара отправляет Change Notification kitchen board его ресторана (ADR 36). Integration Events не публикуются.

## Acceptance

FR-615, AC-778 change [`add-cook-and-courier-pools`](../../../../../../openspec/changes/add-cook-and-courier-pools/).

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

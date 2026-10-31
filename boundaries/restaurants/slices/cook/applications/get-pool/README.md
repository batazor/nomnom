# Get Cook Pool

## Сценарий

- **Тип:** Query.
- **Actor:** Авторизованный сотрудник ресторана (`restaurant#view_orders`).
- **Намерение:** Увидеть линию и загрузку кухни.
- **Результат:** Pool Load: `size`, `onLine`, `busy`, `queued` и невыбывшие повара по имени с текущим заказом; состояние не меняется.

## Границы

- **Владелец:** `restaurants/slices/cook`.
- **Домен:** `domains/cook`.
- **Взаимодействие:** Занятость повара и очередь читаются из Restaurant Orders (`CookWorkload`); запись повара отправляет Change Notification kitchen board его ресторана (ADR 36). Integration Events не публикуются.

## Acceptance

FR-616, AC-781 change [`add-cook-and-courier-pools`](../../../../../../openspec/changes/add-cook-and-courier-pools/).

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

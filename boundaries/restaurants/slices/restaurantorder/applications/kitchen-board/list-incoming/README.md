# List Incoming Orders

## Сценарий

- **Тип:** Query.
- **Actor:** Авторизованный сотрудник ресторана (`restaurant#operate_kitchen`).
- **Намерение:** Увидеть новые заказы, ожидающие решения кухни.
- **Результат:** Заказы в `RECEIVED` по возрастанию `received_at` (затем по id) с позициями, версией и `received_at`.

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Читает read model kitchen board. Это доска «входящие», а не Kitchen Queue: принятый заказ из неё уходит. Сотрудник отвечает на заказ командами Accept Order или Reject Order.

## Acceptance

[FR-586, AC-742, AC-743](../../../../../../../openspec/changes/archive/2026-10-25-close-order-lifecycle-in-kind/specs/kitchen-board/spec.md).

Общий каталог: [Restaurants use cases](../../../../../README.md#use-cases).

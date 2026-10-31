# Get Menu

## Сценарий

- **Тип:** Query.
- **Actor:** Клиент или внутренний actor.
- **Намерение:** Получить актуальное меню ресторана.
- **Результат:** Возвращены актуальные позиции, цены, options/modifiers и доступность.

## Границы

- **Владелец:** `restaurants/slices/menu`.
- **Домен:** `domains/menu`.
- **Взаимодействие:** Читает query model; результат не является snapshot оформленного заказа.

## Acceptance

BDD-сценарии размещаются в `acceptance/get.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).


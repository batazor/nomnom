# List Ready Orders

## Сценарий

- **Тип:** Query.
- **Actor:** Авторизованный сотрудник ресторана.
- **Намерение:** Увидеть заказы, ожидающие передачи курьеру.
- **Результат:** Заказы в `READY` с `ready_at`.

## Границы

- **Владелец:** `restaurants/slices/restaurantorder`.
- **Домен:** `domains/restaurantorder`.
- **Взаимодействие:** Читает read model kitchen board. Информационные данные курьера и факт Pickup — будущая projection событий `delivery`; до неё список содержит все заказы в `READY`.

## Acceptance

BDD-сценарии размещаются в `acceptance/list-ready.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../../README.md#use-cases).

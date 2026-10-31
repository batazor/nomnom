# Set Menu Item Availability

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный управляющий ресторана.
- **Намерение:** Изменить возможность заказать блюдо без редактирования остальных данных.
- **Результат:** Доступность `Menu Item` изменена.

## Границы

- **Владелец:** `restaurants/slices/menu`.
- **Домен:** `domains/menu`.
- **Взаимодействие:** Может записать бизнес-факт через Transactional Outbox.

## Acceptance

BDD-сценарии размещаются в `acceptance/set-item-availability.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).


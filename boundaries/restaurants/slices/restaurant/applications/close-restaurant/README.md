# Close Restaurant

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный управляющий ресторана.
- **Намерение:** Прекратить приём новых заказов.
- **Результат:** `Restaurant` закрыт для новых `Kitchen Order`; уже принятые заказы автоматически не отменяются.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Взаимодействие:** Может записать бизнес-факт через Transactional Outbox.

## Acceptance

BDD-сценарии размещаются в `acceptance/close-restaurant.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).


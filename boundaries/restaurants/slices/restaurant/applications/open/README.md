# Open Restaurant

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный управляющий ресторана.
- **Намерение:** Разрешить приём новых заказов.
- **Результат:** `Restaurant` открыт для новых `Kitchen Order`.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Взаимодействие:** Может записать бизнес-факт через Transactional Outbox.

## Acceptance

BDD-сценарии размещаются в `acceptance/open.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).


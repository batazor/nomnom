# Update Restaurant

## Сценарий

- **Тип:** Command.
- **Actor:** Авторизованный управляющий ресторана.
- **Намерение:** Изменить основные данные ресторана.
- **Результат:** Разрешённые данные `Restaurant` обновлены: `name`.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Взаимодействие:** Проверяет права и применяет инварианты `Restaurant`.

## Acceptance

BDD-сценарии размещаются в `acceptance/update-restaurant.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).


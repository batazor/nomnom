# List Owned Restaurants

## Сценарий

- **Тип:** Query.
- **Actor:** Владелец ресторана.
- **Намерение:** Найти рестораны, которыми он управляет.
- **Результат:** Возвращены открытые и закрытые рестораны владельца без изменения состояния.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Взаимодействие:** Читает query model по `ownerId` — источнику `restaurant#owner`; так как `restaurant#manage = owner`, фильтр и есть authorization. Service subject получает `PERMISSION_DENIED`.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

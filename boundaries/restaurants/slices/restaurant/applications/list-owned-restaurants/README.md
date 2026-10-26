# List Owned Restaurants

## Сценарий

- **Тип:** Query.
- **Actor:** Владелец ресторана.
- **Намерение:** Найти рестораны своего Market, которыми он управляет.
- **Результат:** Возвращены открытые и закрытые рестораны владельца в обязательном Market (`market_id`, ADR 41) без изменения состояния.

## Границы

- **Владелец:** `restaurants/slices/restaurant`.
- **Домен:** `domains/restaurant`.
- **Взаимодействие:** Читает query model по `ownerId` — источнику `restaurant#owner`; так как `restaurant#manage = owner`, фильтр и есть authorization. Service subject получает `PERMISSION_DENIED`; пустой или не slug `market_id` — `INVALID_ARGUMENT`. Индекс `restaurants_owner_market_by_id`.

Общий каталог: [Restaurants use cases](../../../../README.md#use-cases).

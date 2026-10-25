# Design

## Decisions

- `RegisterRestaurant(name, owner_id, address)` → `Restaurant` и `version`. Subject — проверенный caller (оператор платформы); permission `platform:nomnom#register_restaurant`. Идемпотентности у use case нет: повтор создаёт новый ресторан. Ключ идемпотентности — отдельное решение, когда появится клиент.
- `AssignStaff`/`RevokeStaff(restaurant_id, user_id)` → состав персонала и `changed`. Повтор — `changed = false` без записи в SpiceDB.
- Ошибки — существующий маппинг `service.ts`: валидация → `INVALID_ARGUMENT`, нет ресторана → `NOT_FOUND`, нет права → `PERMISSION_DENIED`, SpiceDB недоступен → `UNAVAILABLE`, конфликт версии → `ABORTED`.
- `main.ts`: один SpiceDB-клиент на check и write; `RestaurantAccessControl` — `SpiceDbAccessControl`, writers — `SpiceDbRelationshipWriter`. Хранилище остаётся in-memory процесса.

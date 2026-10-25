# Tasks

## 1. Контракты

- [x] 1.1 `RestaurantOpened`, `RestaurantClosed`, `MenuItemAvailabilityChanged` в `events.proto`, генерация; `npm run proto:check`, `buf breaking --against '.git#branch=main'` (`FR-560`, `FR-561`).

## 2. Публикация

- [x] 2.1 Общие `protobufIntegrationEvents`, `allowlistMapper`, `outboxEventMapperByAggregate`; mapper Kitchen Order на них (`FR-521`, `FR-562`).
- [x] 2.2 Mappers `restaurant-events.ts` и `menu-events.ts` с allowlist; регистрация в слайсах и композиция в `di/restaurants.ts` (`FR-560`, `FR-561`, `FR-562`, `AC-666`).
- [x] 2.3 Use cases Open/Close Restaurant, Set Menu Item Availability и Update Menu Item передают факты в Outbox внутри UoW (без изменений кода).

## 3. Тесты и документация

- [x] 3.1 Unit-тесты mappers, композиции и use case → запись Outbox (`AC-603`, `AC-660`–`AC-666`).
- [x] 3.2 Integration-тест Outbox → relay → Kafka для Open Restaurant и Set Menu Item Availability (пропускается без `MONGODB_URI`, `KAFKA_BROKERS`).
- [x] 3.3 README `restaurants`, `docs/domain-events.md`; `npm run check`, `npm run build`, `npm test`; `openspec validate publish-restaurant-and-menu-events --strict`.

# Proposal

## Why

Register Restaurant, Assign Staff и Revoke Staff со SpiceDB-записью relationships реализованы, но не опубликованы: в `cmd/restaurants/main.ts` их никто не создаёт, поэтому `restaurant#owner` и `restaurant#staff` в окружении не появляются.

## What Changes

- RPC `RegisterRestaurant`, `AssignStaff`, `RevokeStaff` в `nomnom.restaurants.v1.RestaurantsService`.
- `cmd/restaurants/main.ts` собирает эти use cases с `SpiceDbAccessControl` (`RestaurantAccessControl`) и `SpiceDbRelationshipWriter` (`OwnerRelationshipWriter`, `StaffRelationshipWriter`).
- gRPC-тесты на injected subject и integration-тест против SpiceDB.

## Capabilities

### New Capabilities

- `restaurant-administration-api`: gRPC API регистрации ресторана и управления персоналом.

### Modified Capabilities

Нет.

## Impact

`boundaries/restaurants`: proto и сгенерированный код, `infrastructure/grpc/service.ts`, `cmd/restaurants/main.ts`, тесты, README. Клиентам `orders` (генерация из workspace) изменение совместимо.

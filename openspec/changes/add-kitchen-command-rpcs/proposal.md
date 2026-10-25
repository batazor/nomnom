# Proposal

## Why

Use cases кухни `restaurants` (Accept/Reject Order, Start Preparation, Mark Assembled, Mark Ready for Pickup, Report Preparation Delay/Failure) реализованы, а их факты сопоставлены Integration Events и публикуются `restaurants-relay`, но к gRPC подключены только `ReceiveOrderRequest` и `CancelRestaurantOrder`. Поэтому `RestaurantOrderAccepted`, `PreparationStarted`, `RestaurantOrderReady` и остальные факты в Kafka не попадают.

## What Changes

- RPC `AcceptRestaurantOrder`, `RejectRestaurantOrder`, `StartPreparation`, `MarkRestaurantOrderAssembled`, `MarkRestaurantOrderReady`, `ReportPreparationDelay`, `ReportPreparationFailure` в `nomnom.restaurants.v1.RestaurantsService`, общий ответ `RestaurantOrder` с enum `RestaurantOrderState`.
- `cmd/restaurants/main.ts` собирает handlers с `MongoUnitOfWork`, `MongoOutbox` и `restaurantOrderEventMapper`; MVP-адаптеры открытых решений `UnconstrainedAcceptanceConditions` и `FixedPreparationTimeEstimator`.
- gRPC-тесты и integration-тест против MongoDB replica set.

## Capabilities

### New Capabilities

- `kitchen-command-api`: gRPC API команд кухни над `Restaurant Order`.

### Modified Capabilities

Нет.

## Impact

`boundaries/restaurants`: proto и сгенерированный код, `infrastructure/grpc/service.ts`, `slices/kitchenorder/infrastructure/acceptance`, `cmd/restaurants/main.ts`, тесты, README. Изменение proto аддитивно. SpiceDB schema не меняется: используется существующий `restaurant#operate_kitchen`. Маршрут internal gateway для `restaurants` не публикуется, поэтому manifests gateway не меняются.

# Tasks

## 1. gRPC API

- [x] 1.1 `service.proto`: `OpenRestaurant`, `CloseRestaurant`, `CreateMenu`, `AddMenuItem`, `UpdateMenuItem`, `SetMenuItemAvailability` и их messages; `npm run proto:check`, `buf breaking --against '.git#branch=main'`, `npm run generate:proto` (`FR-570`, `FR-571`).
- [x] 1.2 Adapters `infrastructure/grpc/restaurant-commands.ts`, `infrastructure/grpc/menu-commands.ts`; маппинг `invalid_menu`, `invalid_money` → `INVALID_ARGUMENT`; регистрация handlers в `slices/restaurant/di`, `slices/menu/di`, `di/restaurants.ts`.
- [x] 1.3 Тесты `test/grpc-restaurant-menu-commands.test.ts` (успех, повтор, `PERMISSION_DENIED`, `NOT_FOUND`, `ALREADY_EXISTS`, `INVALID_ARGUMENT`) и env-gated `test/grpc-restaurant-menu-integration.test.ts` (HTTP/2, MongoDB Outbox, `traceparent`) (`AC-711`…`AC-716`).
- [x] 1.4 Route `restaurants-v1-management` в `ops/kind/manifests/gateway/gateway.yaml`, таблица в `ops/kind/README.md`.

## 2. Reconciliation

- [x] 2.1 `RestaurantMembers.version`, `recordAuthorizationRevision` → `{ from, to }`; missing-кандидат по версии, перенос после собственной записи revision (`FR-445`, `AC-710`).
- [x] 2.2 Тесты `test/reconcile.test.ts` (повторное назначение и снятие между проходами, сброс grace при другой версии), `test/mongodb-integration.test.ts`.

## 3. Документация и проверка

- [x] 3.1 README `restaurants`.
- [x] 3.2 `npm run check`, `npm run build`, `npm test` с MongoDB, Kafka и SpiceDB; `openspec validate add-restaurant-menu-command-rpcs --strict`.

# Tasks

- [x] 1.1 `orders`: `FactRestaurantAccepted`, `Order.Confirm(occurredAt)` записывает факт с переходом; confirmorder передаёт `OccurredAt`; тесты домена, use case и gRPC `GetOrder` (`FR-105`, `AC-563`).
- [x] 1.2 BFF: `restaurant_accepted` → `CONFIRMED`; тест (`FR-495`, `AC-564`); README BFF.
- [x] 1.3 README `orders` (список фактов); `go test`, `make lint` в `orders`, `go test` и `npm run check` в BFF; `openspec validate record-order-confirmation-time --strict`.

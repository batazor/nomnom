# Tasks

- [x] 1.1 `outbound.IntegrationMapper`; Outbox только сохраняет; Forwarder отдельно; `OrderCompleted.CompletedAt` (`FR-544`, `AC-634`).
- [x] 1.2 `order.CourierAssignment` в домене, контракт хранения в домене, `application.AcceptOnce` (`FR-546`, `AC-636`).
- [ ] 1.3 `application.Classify` и таблицы адаптеров с тестом полноты (`FR-545`, `AC-635`).
- [ ] 1.4 Роли хранилища вместо `Store`; декоратор метрик в `observability`.
- [ ] 1.5 Порты соседей в `slices/fulfillment/applications/neighbours`.
- [ ] 1.6 Identity: `jwks`, `serviceTokenVerifier`, `userAssertionVerifier`; общие векторы проходят.
- [ ] 1.7 README; `go test ./...`, `go vet ./...`, `make lint`, `go tool wire ./di`; `openspec validate refactor-orders-responsibilities --strict`.

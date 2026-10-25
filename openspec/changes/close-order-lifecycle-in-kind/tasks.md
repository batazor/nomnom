# Tasks

## 1. delivery

- [ ] 1.1 proto `nomnom.delivery.v1`: RPC и сообщения API курьера (design, раздел 2); генерация.
- [ ] 1.2 gRPC handlers API курьера поверх существующих use cases; тесты AC-730–AC-733.
- [ ] 1.3 Use case Dispatch Deliveries и выбор ожидающих доставок без pending-предложения в memory и PostgreSQL; тесты AC-734–AC-736.
- [ ] 1.4 Worker Dispatch в процессе `delivery` (`OFFERS__DISPATCH_INTERVAL`), Helm values и README.

## 2. restaurants

- [ ] 2.1 proto и gRPC `GetKitchenQueue` поверх существующего query; тесты AC-737, AC-738.
- [ ] 2.2 Query List Incoming Orders (memory и MongoDB) и gRPC `ListIncomingOrders`; тесты AC-742, AC-743.

## 3. kind

- [x] 3.1 Skaffold-релизы и values `orders-worker`, `orders-forwarder`; AC-739.
- [x] 3.2 Gateway routes: команды кухни и `GetKitchenQueue`, API курьера, `RegisterRestaurant`, `AssignStaff`, `RegisterCourier`; тест реестра маршрутов.
- [x] 3.3 Fixture `e2e-driver`, kind-values allowlist и `userDelegates` `restaurants`/`delivery`; AC-741.
- [x] 3.4 `ops/kind/authz/bootstrap-kind.yaml` с `platform:nomnom#operator@user:kind-operator`, подключение в `scripts/authz.sh`.
- [ ] 3.5 Chainsaw `e2e/order-lifecycle`: AC-740; прогон в `nomnom-test`.

## 4. Документация

- [ ] 4.1 README `delivery`, `restaurants`, `ops/kind/README.md`, `docs/use-cases.md` (момент начала поиска курьера решён Dispatch).
- [ ] 4.2 `openspec validate close-order-lifecycle-in-kind --strict`.

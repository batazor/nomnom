# Tasks

## 1. Command RPC

- [x] 1.1 Добавить `PlaceOrder`, `RequestOrderCancellation` и их messages в `service.proto`; выполнить `make proto-generate` и `buf lint` (`FR-464`, `FR-465`).
- [x] 1.2 Добавить `application.ErrDependencyUnavailable`, обернуть им `spicedb.ErrUnavailable`; дополнить `transportError` (`AC-509`, `AC-511`, `AC-513`).
- [x] 1.3 Добавить `neighbours.UnavailableCatalog`, `system.Clock`, `system.OrderIDs`; проверить unit-тестами (`AC-509`, `AC-515`).
- [x] 1.4 Подключить handlers в `slices/order/di.NewModule` с общей cancellation policy; lazy Temporal client и `fulfillorder.Client` в `di` (`FR-466`, `AC-514`).
- [x] 1.5 Тесты gRPC-сервиса поверх bufconn (`AC-508`…`AC-513`).

## 2. Relationships

- [x] 2.1 Расширить `definition order` и `validation.yaml`; `make authz-validate` (`FR-467`, `AC-517`).
- [x] 2.2 `GrantOrderRelationships` пишет customer и restaurant одним запросом (`FR-468`, `AC-516`).
- [x] 2.3 Миграция `00002_courier_assignment.sql`; `CourierAssignment` (`FOR UPDATE`) и `RememberCourierAssignment` в PostgreSQL и memory store (`AC-523`).
- [x] 2.4 `Reflector.ReflectWith`; Record Courier Assignment пишет relationships в Inbox UoW, пропускает устаревшие события; `spicedb.AssignCourier` (`FR-469`, `AC-518`…`AC-522`).
- [x] 2.5 `orders-consumer` получает SpiceDB writer; mapper передаёт `delivery_id`/`courier_id`.

## 3. Reconciliation

- [x] 3.1 Обобщить `reconcile` на `reconcile.Relations` и `postgres.Relation` (`FR-470`, `AC-524`).

## 4. Документация и проверка

- [x] 4.1 Обновить README `orders` и `docs/security.md`; зафиксировать known gap снятия курьера.
- [x] 4.2 `go vet ./...`, `make lint`, `go test ./...`; integration-тесты PostgreSQL, SpiceDB, reconcile и Kafka против временных контейнеров.

## 5. Снятие курьера

- [x] 5.1 `delivery`: `CourierUnassignedFromDelivery`, `DeliveryCancelled` в `events.proto`, mapper `DeliveryIntegrationEvents`, тесты event_id и relay (`FR-475`, `AC-532`, `AC-533`).
- [x] 5.2 `orders`: `make proto-generate`; use case Record Courier Release, `spicedb.ReleaseCourier`, NULL-семантика `RememberCourierAssignment` (`FR-476`, `AC-534`…`AC-537`).
- [x] 5.3 Reconciler удаляет остаток снятого курьера (`FR-477`, `AC-538`); README, `docs/security.md`.

## Workflow follow-up

- User identity через BFF, после чего Place Order станет доступен клиентам.
- RPC меню в `restaurants` и production `Catalog`.
- После review реализации архивировать change через OpenSpec archive workflow.

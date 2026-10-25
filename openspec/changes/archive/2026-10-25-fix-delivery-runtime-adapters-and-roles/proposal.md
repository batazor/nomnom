# Proposal

## Why

Ревью SOLID нашло в `delivery` два дефекта production-сборки и несколько смешанных ответственностей:

- `di/server.rs` → `di/infrastructure.rs::infrastructure()` использует тестовые адаптеры: `SequentialIdGenerator::new("delivery")` начинает счётчик с 1 при каждом старте, поэтому после рестарта или при двух репликах id `Delivery` и offer совпадают с уже сохранёнными в PostgreSQL; `StaticCourierLocator::new(Vec::new())` пуст, поэтому Find Courier и Replan никогда не находят кандидата.
- `ProcessStore` объединяет около десяти ролей, `SharedStore` и `DeliveryPorts` (13 методов доступа) работают как service locator: потребитель получает всё хранилище ради одной роли (ISP).
- `replan_delivery` собирает три других handler'а, каждый из которых заново проверяет права и открывает свою транзакцию (SRP); работает лишь потому, что `coordinates_delivery` случайно даёт все нужные права.
- «Снять назначение» написано в четырёх use cases по-разному: где-то relationship отзывается, где-то нет (FR-447).
- `infrastructure/grpc/identity.rs` объединяет чтение файлов, разбор JWKS и allowlist, проверку service token и user assertion (SRP).

## What Changes

- Production-сборка получает UUID-генератор идентификаторов и локатор курьеров поверх `CourierLocationStore` (последние позиции доступных курьеров); memory-адаптеры остаются только в тестовых сборках. `infrastructure()` принимает генератор и локатор параметрами.
- Роли хранилища: `TransactionalStore` (UoW, репозитории, Outbox), `DeliveryReadModels`, `ReconciliationStore`, `RevisionReader` — каждая разрешается отдельно поверх одного `Arc<PostgresStore>`; `DeliveryPorts` сужается до того, что нужно use cases.
- Прикладные сервисы без авторизации: `end_assignment`, `select_candidate`, `make_offer`; handlers — validate → authorize → UoW → сервис; Replan вызывает сервисы под одной проверкой `ReplanDelivery` в одной транзакции.
- Одна функция снятия назначения с явным выбором отзыва relationship; расхождение confirm/report-failure (без отзыва) сверяется с FR-447 и фиксируется.
- `identity` делится на `jwks`, `service_token`, `user_assertion`, `config`; resolver только соединяет verifier'ы.

Не входит: изменение gRPC-контрактов, схемы хранения, политики назначения.

## Capabilities

### Modified Capabilities

- `delivery-assignment`: уникальные идентификаторы и реальный поиск кандидатов в production.
- `source-module-structure`: роли хранилища и прикладные сервисы delivery.

## Impact

`boundaries/delivery` (di, infrastructure, slices/delivery/applications, README), `docs/unit-of-work.md` при уточнении FR-447.

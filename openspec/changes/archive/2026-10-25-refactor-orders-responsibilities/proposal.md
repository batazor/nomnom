# Proposal

## Why

Ревью SOLID `orders`:

- `slices/order/infrastructure/postgres/outbox.go` пишет Outbox и одновременно переводит Domain Events в Integration Events (type switch с импортом Protobuf), вычисляет `event_id` и собирает Forwarder; `OrderCompleted` получает момент `time.Now()` в адаптере, потому что доменное событие его не несёт.
- Модель назначения курьера и её правила («старое событие устарело», «то же назначение», «снятие сохраняет время») живут в use case `recordcourierassignment`; `recordcourierrelease`, SpiceDB, PostgreSQL и memory импортируют этот use case ради типа. Release повторяет протокол Inbox из `Reflector`.
- Классификация ошибок записана четыре раза (gRPC, Kafka-обработчик, Temporal Activities, метрики), таблицы уже расходятся (`GuardFailed` есть не везде).
- `Store` в `slices/order/di` — около 14 методов шести областей; потребители используют часть (ISP). Декоратор метрик живёт в composition root.
- Порты и DTO соседей (Payments, Restaurants, Delivery) объявлены в Temporal-адаптере, поэтому `neighbours` и `grpcclient` зависят от пакета `temporal`.
- `infrastructure/grpc/identity.go` смешивает разбор JWKS, проверку service token и user assertion.

## What Changes

- `IntegrationMapper` в `slices/order/infrastructure/messaging/outbound`; PostgreSQL Outbox только сохраняет сообщения; Forwarder — отдельно. `OrderCompleted` получает `CompletedAt` из домена.
- `CourierAssignment` — value object в `domains/order` с правилами; контракт хранения назначений — в домене; общий `application.AcceptOnce` для протокола Inbox в UoW, им пользуются Reflector и Release.
- `application.Classify(err) ErrorKind` и таблицы адаптеров (gRPC-коды, класс ошибки consumer'а, retryability, метрики); дополнительные детали (metadata `ORDER_NOT_ORDERABLE`) остаются в адаптере.
- Роли хранилища вместо одного `Store` (`CommandStore`, `ReadModels` и т. п.); декоратор метрик переезжает в `observability`.
- Порты и DTO соседей — в `slices/fulfillment/applications/neighbours`; Activities: загрузить → построить запрос → вызвать → классифицировать.
- Identity: отдельные `jwks`, `serviceTokenVerifier`, `userAssertionVerifier`; resolver соединяет.

Не входит: изменение внешних контрактов, кроме времени `OrderCompleted`.

## Capabilities

### Modified Capabilities

- `order-use-cases`: `OrderCompleted` несёт момент завершения.
- `source-module-structure`: границы ответственности orders.

## Impact

`boundaries/orders` (domains, applications, infrastructure, di, README).

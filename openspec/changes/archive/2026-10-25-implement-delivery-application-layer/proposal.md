# Proposal

## Why

В `delivery` реализованы только in-memory агрегаты `Courier` и `Delivery` и policies `CourierEligibleForDelivery` / `DeliveryOfferAcceptable`; ни один Command или Query из `boundaries/delivery/README.md` не исполняется. Без application-слоя нельзя проверить назначение курьера, Pickup и Delivery Completion как сценарии с authorization, транзакционной границей и публикацией бизнес-фактов, а будущие gRPC/Temporal/Kafka адаптеры не имеют контракта, к которому подключаться.

## What Changes

- Добавить application handlers (`Input` / `Output` / `execute`) для всех 14 use cases `delivery`: Register Courier, Set Courier Availability, Update Courier Location, Get Courier Current Delivery (slice `courier`); Create Delivery, Find Courier, Offer Delivery, Accept Delivery Offer, Reject Delivery Offer, Confirm Pickup, Confirm Delivery, Reassign Courier, Report Delivery Failure, List Available Delivery Offers (slice `delivery`).
- Расширить доменную модель ровно настолько, насколько требуют сценарии:
  - `Courier`: состояние `Unavailable` и строгие переходы доступности; value object `Location`;
  - `Delivery`: triggers `Unassign` и `Fail`, состояние `Failed` с причиной, проверка назначенного курьера для Pickup/Completion/Failure, domain events для Assignment, Reassignment, Pickup, Completion и Failure;
  - новый aggregate `DeliveryOffer` с собственной табличной FSM (`Pending → Accepted | Rejected | Withdrawn`) и вызовом `DeliveryOfferAcceptable` при принятии;
  - контракты репозиториев `CourierRepository`, `DeliveryRepository`, `DeliveryOfferRepository` рядом с моделями.
- Объявить порты: shared `UnitOfWork`, `Clock`, `IdGenerator`, `Subject`/`AuthorizationError` в `pkg/application`; authorization port каждого слайса, Outbox, `CourierLocator`, хранилище позиции и query read models — рядом с потребляющими сценариями в `applications/`.
- Добавить in-memory адаптеры всех портов (общее хранилище с транзакционной семантикой commit/rollback, статическая authorization, управляемые clock и id generator, настраиваемый locator) в `infrastructure/memory`.
- Добавить unit-тесты на каждый handler: успешный исход, authorization deny/unavailable без изменения состояния, доменные отказы, идемпотентные повторы и откат UoW.
- Обновить статус реализации в `boundaries/delivery/README.md`.
- **BREAKING** (внутренний Rust API `delivery`, внешних потребителей нет): `Delivery::restore`, `Delivery::confirm_pickup`, `Delivery::complete` получают дополнительные параметры; `DomainEvent::DeliveryCompleted` дополняется `order_id` и `courier_id`.

Вне scope: gRPC/protobuf, HTTP-транспорт и подключение к `cmd/delivery`, выбор и реализация хранилища, Kafka-клиенты и Outbox relay, SpiceDB adapter и schema, Helm/ops, BDD runner и `.feature` файлы, расчёт геопозиции/зон/расстояний.

Открытые продуктовые вопросы не решаются этим change:

- момент начала поиска курьера — `Create Delivery`, `Reject Delivery Offer` и `Reassign Courier` не запускают Find Courier автоматически; запуск остаётся за вызывающим процессом;
- retryability причин неудачи доставки — `Report Delivery Failure` сохраняет и публикует причину без классификации; решение о повторе остаётся за `orders`;
- частота, точность и retention позиции курьера — не ограничиваются; хранится последняя позиция;
- численные значения радиуса, лимита активных назначений и срока offer — параметр `AssignmentPolicy`, а не константа домена.

## Capabilities

### New Capabilities

- `courier-availability`: регистрация курьера, управление доступностью для новых назначений, последняя известная позиция и чтение текущей доставки курьером.
- `delivery-assignment`: создание Delivery по запросу fulfillment, поиск кандидата, Delivery Offer, принятие и отклонение offer, Reassignment и список доступных offers курьера.
- `delivery-execution`: Pickup, Delivery Completion и Delivery Failure назначенной доставки с публикацией бизнес-фактов через Outbox.

### Modified Capabilities

Нет: в `openspec/specs/` нет спецификаций `delivery`.

## Impact

- `boundaries/delivery/slices/courier`: расширение `domains/courier`, новые `applications/*` modules.
- `boundaries/delivery/slices/delivery`: расширение `domains/delivery`, новый aggregate `DeliveryOffer`, новые `applications/*` modules.
- `boundaries/delivery/pkg`: новые `application` и `persistence` modules без бизнес-логики.
- `boundaries/delivery/infrastructure/memory`: in-memory адаптеры для тестов.
- Новых зависимостей в `Cargo.toml` нет; HTTP binary `cmd/delivery` не меняется.
- Общие документы (`docs/unit-of-work.md`, `docs/use-cases.md`, `GLOSSARY-MAP.md`) этим change не редактируются; предлагаемые правки перечисляются в отчёте реализации.

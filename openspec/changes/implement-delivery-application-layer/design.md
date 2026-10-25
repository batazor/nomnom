# Design

## Context

См. [proposal.md](proposal.md) — Why. Требования — в `specs/courier-availability`, `specs/delivery-assignment`, `specs/delivery-execution`.

Текущее состояние `boundaries/delivery` (Rust 2024, без async runtime и без зависимостей кроме `config`/`serde`):

- `slices/courier/domains/courier`: `Courier { id, status: Available | Busy }` со строгими `mark_busy` / `release`, policy `courier_eligible_for_delivery`;
- `slices/delivery/domains/delivery`: `Delivery` с табличной FSM `Waiting → Assigned → PickedUp → Delivered`, `Address`, `DomainEvent::DeliveryCompleted`, policy `delivery_offer_acceptable`;
- `tests/domain_specifications.rs` — интеграционные тесты policies, которые должны остаться зелёными;
- каталоги `applications/<usecase>/` содержат только README.

Ограничения: зависимости между слоями по [boundaries/README.md](../../../boundaries/README.md#направление-зависимостей); repository traits — рядом с моделью в `domains/`, порты интеграций и чтения — в `applications/`, UoW port — в `pkg/application` ([unit-of-work.md](../../../docs/unit-of-work.md#размещение)); authorization выполняется до доменной операции и fail closed ([security.md](../../../docs/security.md)); FSM чистая и табличная ([fsm.md](../../../docs/fsm.md)); specifications получают `now` и snapshot значениями ([specification-pattern.md](../../../docs/specification-pattern.md)).

## Goals / Non-Goals

**Goals:**

- один module на use case с `Input`, `Output`, handler и `execute`; handlers создаются в тестах с явно переданными портами;
- все state-changing правила остаются в aggregates/FSM/specs, handler только координирует authorization → load → domain → persist → Outbox;
- транзакционная семантика UoW проверяема в unit-тестах in-memory адаптером (commit, rollback, сбой commit);
- открытые продуктовые вопросы не закрываются кодом: поведение вынесено в параметры и явно перечислено ниже.

**Non-Goals:**

- async API, выбор storage/driver, optimistic concurrency по `version` в реальном хранилище;
- mapping ошибок в gRPC codes (таблица README применяется будущим adapter'ом; варианты ошибок спроектированы так, чтобы маппинг был однозначным);
- Integration Event контракты, Event Envelope, `event_id`, `aggregate_version` — ответственность infrastructure Outbox adapter'а;
- BDD `.feature` и `cucumber` runner: AC получают идентификаторы, executable scenarios — отдельный change.

## Decisions

### Размещение кода

```text
pkg/application/     Subject, AuthorizationError, Clock, IdGenerator, UnitOfWork(+Error)
pkg/persistence/     RepositoryError — общий технический тип ошибок хранилища
slices/courier/domains/courier/      Courier (+Unavailable), Location, CourierRepository
slices/courier/applications/         authorization port, error, 4 use-case modules
slices/delivery/domains/delivery/    Delivery (+Unassign/Fail), DeliveryOffer, repositories
slices/delivery/applications/        authorization, outbox, courier_locator, policy, error, 10 use-case modules
infrastructure/memory/               in-memory adapters всех портов
```

`RepositoryError` вынесен в `pkg/persistence`, потому что его используют контракты репозиториев обоих слайсов и он не имеет бизнес-владельца; `domains/` зависит только от этого технического enum, а не от `applications/` или драйверов. Альтернатива — отдельный enum в каждом domain module — отклонена: удваивает `From`-преобразования без выигрыша в смысле.

Ошибки application — один enum на слайс (`CourierApplicationError`, `DeliveryApplicationError`) с вариантами, однозначно соответствующими таблице ошибок README (not found, invalid, precondition, already exists, permission denied, dependency unavailable, internal). Отдельный enum на каждый handler отклонён: 14 почти одинаковых типов и дублирование маппинга в будущем adapter'е.

Shared in-memory адаптеры размещены в boundary-level `infrastructure/memory`, как и будущая shared реализация UoW: UoW должен видеть все репозитории и Outbox одного хранилища, чтобы откатывать их вместе.

### Синхронный API портов

Порты и handlers синхронные: в контексте нет async runtime, а выбор хранилища и драйвера ещё не сделан. UoW — `run(work: FnOnce() -> Result<T, E>) -> Result<T, E>` с `E: From<UnitOfWorkError>`; это та же семантика scope, что в [unit-of-work.md](../../../docs/unit-of-work.md#контракт). Переход на `async fn` выполняется вместе с выбором хранилища и затрагивает только сигнатуры. Handlers принимают зависимости как `&dyn Trait`, кроме UoW (generic-метод не object-safe, поэтому handler generic по `U: UnitOfWork`).

### Authorization

Каждый слайс объявляет свой узкий port: `CourierAuthorization` / `DeliveryAuthorization` с enum permissions по use cases и enum resources (`Platform`, `Courier(id)`, `Delivery(id)`). `Subject` (`User` | `Service`) и `AuthorizationError` (`Denied` | `Unavailable`) общие в `pkg/application`. Проверка выполняется первой; `Denied` → `PermissionDenied`, `Unavailable` → `DependencyUnavailable` (fail closed).

Курьерские действия (accept/reject/list offers, Pickup, Completion, failure от курьера) проверяют право subject действовать от имени `Courier(courier_id)`, а принадлежность offer/Delivery этому курьеру проверяет домен. Альтернатива — проверка только по `Delivery(id)` через relation `delivery#courier` — отклонена: relation выводится асинхронно через Outbox, и после Reassignment устаревшая relation позволила бы прежнему курьеру действовать от имени нового. Связь subject ↔ courier остаётся за authorization adapter.

### Доменные расширения

- `CourierStatus::Unavailable`; `mark_available` / `mark_unavailable` строгие (`AlreadyAvailable`, `AlreadyUnavailable`, `Busy`); `mark_busy` из `Unavailable` → `CourierError::Unavailable`; `ensure_available` для проверок offer/candidate без изменения состояния. Повтор той же доступности handler превращает в no-op (`changed: false`).
- `Location` — value object с конечными координатами в диапазонах широты [-90, 90] и долготы [-180, 180]. Позиция не входит в aggregate `Courier` (минимальная модель README исключает координаты) и хранится портом `CourierLocationStore` без UoW как высокочастотная одиночная запись.
- `Delivery`: состояние `Failed`, triggers `Unassign` (`Assigned → Waiting`) и `Fail` (`Assigned | PickedUp → Failed`); таблица 5×5 с тестом полноты. `confirm_pickup`, `complete`, `fail` проверяют, что действует назначенный курьер (`DeliveryError::CourierNotAssigned`); `ensure_assignable` выполняет dry-run trigger `Assign` для Find Courier / Offer Delivery, чтобы правило «назначить можно только waiting» оставалось в таблице. `DeliveryFailureReason` — непустая нормализованная строка без классификации.
- Domain events `CourierAssignedToDelivery`, `CourierUnassignedFromDelivery`, `DeliveryPickedUp`, `DeliveryCompleted`, `DeliveryFailed { reason }` содержат `delivery_id`, `order_id`, `courier_id`: эти данные нужны всем consumers факта, поэтому они в Domain Event, а не в Application DTO.
- `DeliveryOffer` — отдельный aggregate (`id`, `delivery_id`, `courier_id`, `offered_at`, `expires_at`, `state`) с FSM `Pending → Accepted | Rejected | Withdrawn` (таблица 4×3, повтор → `AlreadyInState`). Истечение — время, а не сохранённое состояние: `accept` вычисляет `DeliveryOfferAcceptable` с `now`, `expires_at` и snapshot курьера; `reject` отклоняет просроченный offer. Порядок в `accept`: адресат → specification → FSM, как в [fsm.md](../../../docs/fsm.md#domain-methods-и-domain-events).

### Транзакционные границы

| Use case | UoW | Состав |
| --- | --- | --- |
| Accept Delivery Offer | да | offer + прочие offers доставки + `Delivery` + `Courier` + Outbox |
| Reassign Courier | да | `Delivery` + `Courier` + Outbox |
| Confirm Pickup | да | `Delivery` + Outbox |
| Confirm Delivery, Report Delivery Failure | да | `Delivery` + `Courier` + Outbox |
| Create Delivery, Offer Delivery, Register Courier, Set Availability, Reject Offer | нет | одиночная запись; идемпотентность через уникальность в репозитории |
| Update Courier Location | нет | одиночная запись в location store |
| Find Courier, queries | нет | только чтение |

Snapshot курьера для Accept (`CourierLocator`) и `now` получаются до UoW, как требует правило «только локальное хранилище внутри транзакции». Handler забирает события через `pull_events()` и передаёт в порт `DeliveryOutbox` только факты своего сценария; `aggregate_version` и `event_id` добавляет infrastructure adapter.

In-memory UoW сериализует транзакции глобальным lock, снимает snapshot состояния перед `work` и восстанавливает его при ошибке `work` или при инъецированном сбое commit. Вложенные scopes им не поддерживаются — ни один handler их не открывает.

### Идемпотентность

- Create Delivery: ключ — `Order ID` (уникальность в `DeliveryRepository::add`); совпадающий повтор возвращает существующую `Delivery`, гонка двух вставок разрешается повторным чтением после `Conflict`.
- Offer Delivery: открытый pending offer той же пары возвращается вместо нового.
- Accept / Reject: повтор тем же курьером над уже accepted / rejected offer — успешный no-op без записи и без Outbox.
- Set Availability: повтор той же доступности — no-op.
- Confirm Pickup / Confirm Delivery: `AlreadyInState` при повторе назначенным курьером — успешный no-op без Outbox (повтор клиента или Activity после commit); повтор другим курьером остаётся отказом.
- Reassign Courier и Report Delivery Failure строгие: повтор возвращает precondition failure, и вызывающий процесс трактует его по своему шагу.

### Find Courier

Кандидаты и их вне-state данные (зона, расстояние до pickup, число активных назначений) поставляет порт `CourierLocator`; геометрия и зоны не реализуются. Handler загружает каждый `Courier` (state проверяет aggregate, а не spec), исключает курьеров с любым предыдущим offer этой `Delivery`, вычисляет `CourierEligibleForDelivery` с параметрами `AssignmentPolicy` и выбирает минимальное расстояние, при равенстве — меньший `CourierId`. Результат возвращается вызывающему и не сохраняется: README называет его «зафиксированным», но отдельная запись результата поиска не входит в минимальную модель.

### Допущения по открытым вопросам

- **Начало поиска курьера.** Ни Create Delivery, ни Reject, ни Reassign не вызывают Find Courier. Последовательность Find → Offer → Accept запускает внешний процесс назначения (Temporal Activity или будущий assignment process); его триггер — открытый вопрос `docs/use-cases.md`.
- **Retryable причины неудачи.** `Failed` терминален для этой `Delivery`; причина публикуется как есть, решение о повторе принимает `orders`. Поскольку ключ идемпотентности Create Delivery — `Order ID`, повторная доставка того же `Order` после failure потребует отдельного решения (например, номер попытки в ключе) вместе с ответом на этот вопрос.
- **Failure из waiting.** Неудача фиксируется только для assigned/picked up `Delivery`; отказ от поиска для waiting `Delivery` — решение `orders` и не моделируется как `Failed`.
- **Освобождение курьера при failure.** Курьер становится available при failure, как при Completion; это расширяет состав UoW `report_delivery_failure` из `docs/unit-of-work.md` (`Delivery + Outbox`) на `Courier`.
- **Начальная доступность.** Новый `Courier` available — сохраняется существующее поведение `Courier::new`.
- **Unavailable при busy.** Busy курьер не может стать unavailable; «уйти после текущей доставки» не моделируется.
- **Позиция.** Хранится последняя позиция с временем clock `delivery`; частота, точность, retention и порядок устаревших обновлений не ограничиваются.
- **Численные параметры.** Радиус, лимит активных назначений и TTL offer — поля `AssignmentPolicy`, передаваемые в handler; значения выбирает composition root.
- **Повторный offer.** Курьер, уже получавший offer этой доставки (в любом состоянии), не выбирается Find Courier повторно.

## Risks / Trade-offs

- [Сериализующий lock in-memory UoW скрывает гонки] → Unit-тесты проверяют только последовательное принятие (AC-324); конкурентное принятие проверяется integration tests выбранного хранилища через unique constraint/версию, как требует `docs/unit-of-work.md#delivery`.
- [Синхронные порты придётся переписать на async] → Сигнатуры узкие и локализованы в `pkg/application` и `applications/*`; доменная модель не меняется.
- [Изменение сигнатур `Delivery`] → Потребителей вне `delivery` нет; обновляются только локальные unit tests домена.
- [Find Courier загружает каждого кандидата по одному] → Допустимо для in-memory и малого числа кандидатов; batch-загрузка добавляется в порт вместе с реальным хранилищем.
- [Без `version` в aggregate нет optimistic concurrency] → Требование к storage adapter'у зафиксировано в docs; in-memory адаптер сериализует транзакции.

## Migration Plan

Внутреннее изменение библиотеки без deploy-эффекта: HTTP binary не подключает handlers. Откат — revert change.

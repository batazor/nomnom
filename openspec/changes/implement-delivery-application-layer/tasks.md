# Tasks

## 1. Shared ports

- [x] 1.1 Добавить `pkg/application` (`Subject`, `AuthorizationError`, `Clock`, `IdGenerator`, `UnitOfWork`, `UnitOfWorkError`) и `pkg/persistence` (`RepositoryError`) с `Display`/`Error`.

## 2. Доменные расширения

- [x] 2.1 `Courier`: состояние `Unavailable`, строгие `mark_available`/`mark_unavailable`, `ensure_available`, новые `CourierError`; value object `Location`; `CourierRepository`; unit tests.
- [x] 2.2 `Delivery`: `Failed`, triggers `Unassign`/`Fail`, таблица 5×5 с тестом полноты, `DeliveryError::CourierNotAssigned`, `DeliveryFailureReason`, `ensure_assignable`, domain events с `order_id`/`courier_id`; `DeliveryRepository`; обновить unit tests.
- [x] 2.3 Aggregate `DeliveryOffer` с FSM `Pending → Accepted | Rejected | Withdrawn`, вызовом `DeliveryOfferAcceptable` в `accept`, проверкой адресата и срока; `DeliveryOfferRepository`; unit tests.

## 3. Application ports и in-memory адаптеры

- [x] 3.1 Порты слайса `courier`: `CourierAuthorization`, `CourierApplicationError`, `CourierLocationStore`, read model текущей доставки.
- [x] 3.2 Порты слайса `delivery`: `DeliveryAuthorization`, `DeliveryApplicationError`, `DeliveryOutbox`, `CourierLocator`, `AssignmentPolicy`, read model доступных offers.
- [x] 3.3 `infrastructure/memory`: общее хранилище (репозитории, Outbox, location store, read models, UoW с rollback и инъекцией сбоя commit), статическая authorization, управляемые clock и id generator, настраиваемый locator; unit tests UoW.

## 4. Use cases слайса `courier`

- [x] 4.1 Register Courier (FR-301, AC-302, AC-303) и authorization fail closed (FR-300, AC-300, AC-301) с unit tests.
- [x] 4.2 Set Courier Availability (FR-302, AC-304..AC-306) с unit tests.
- [x] 4.3 Update Courier Location (FR-303, AC-307..AC-309) с unit tests.
- [x] 4.4 Get Courier Current Delivery (FR-304, AC-310, AC-311) с unit tests.

## 5. Use cases слайса `delivery`

- [x] 5.1 Create Delivery (FR-306, AC-313..AC-316) с unit tests.
- [x] 5.2 Find Courier (FR-307, AC-317..AC-319) с unit tests.
- [x] 5.3 Offer Delivery (FR-308, AC-320..AC-322) с unit tests.
- [x] 5.4 Accept Delivery Offer (FR-305, FR-309, FR-310, AC-312, AC-323..AC-328) с unit tests.
- [x] 5.5 Reject Delivery Offer (FR-311, AC-329, AC-330) с unit tests.
- [x] 5.6 Reassign Courier (FR-312, AC-331, AC-332) с unit tests.
- [x] 5.7 List Available Delivery Offers (FR-313, AC-333, AC-334) с unit tests.
- [x] 5.8 Confirm Pickup (FR-314, FR-315, AC-335..AC-337) с unit tests.
- [x] 5.9 Confirm Delivery (FR-316, FR-318, AC-338, AC-339, AC-343) с unit tests.
- [x] 5.10 Report Delivery Failure (FR-317, AC-340..AC-342) с unit tests.

## 6. Документация и проверка

- [x] 6.1 Обновить статус реализации в `boundaries/delivery/README.md` и README затронутых use cases (порты, допущения).
- [x] 6.2 Выполнить `make check` в `boundaries/delivery`, `openspec validate implement-delivery-application-layer --strict` и `git diff --check`; подтвердить, что `cmd/delivery`, `Cargo.toml` и файлы вне `boundaries/delivery` и этого change не изменены.

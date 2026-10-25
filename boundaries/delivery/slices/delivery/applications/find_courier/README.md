# Find Courier

## Сценарий

- **Тип:** Command.
- **Actor:** Процесс назначения `delivery`.
- **Намерение:** Подобрать подходящего доступного курьера.
- **Результат:** Выбран кандидат либо зафиксировано отсутствие кандидата.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Оркестрация может запустить поиск, но алгоритм и eligibility принадлежат `delivery`.

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(subject, input)`; требования FR-307. Без авторизации выбор выполняет сервис `select_candidate`, его же вызывает Replan Delivery.
- **Порты:** `DeliveryAuthorization` (`FindCourier` над `Delivery(id)`), `DeliveryRepository`, `CourierAssignment` (порт слайса `courier`), `DeliveryOfferRepository`, `CourierLocator`, `AssignmentPolicy`.
- **Транзакция:** Только чтение: результат возвращается вызывающему процессу и не сохраняется.
- **Допущение:** Выбирается минимальное расстояние до pickup, при равенстве — меньший `CourierId`; курьеры, уже получавшие offer этой доставки, исключаются.

## Acceptance

BDD-сценарии размещаются в `acceptance/find_courier.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).


# Advance Assignment

## Сценарий

- **Тип:** Command.
- **Actor:** Assignment Workflow доставки (`delivery-worker`).
- **Намерение:** Продвинуть поиск курьера на шаг и сказать workflow, чего ждать дальше.
- **Результат:** `finished` (Pickup или закрытие), `assigned`, `offered` с моментом истечения открытого offer либо `no_candidate`; ожидающей доставке без открытого offer предложен курьер, которому её ещё не предлагали.

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery`.
- **Взаимодействие:** Вызывается Activity `advance` workflow `delivery:<delivery_id>` ([ADR 2](../../../../../docs/adr/0002-temporal-для-назначения-курьера.md)); алгоритм выбора — Find Courier (`select_candidate`), offer — Offer Delivery (`make_offer`).

## Реализация

- **Handler:** `mod.rs` — `Input`, `Output`, `Handler::execute(input)`; требования FR-704, FR-705, FR-711.
- **Порты:** `DeliveryRepository`, `DeliveryOfferRepository`, `OffersOfDelivery`, `CourierAssignment`, `CourierLocator`, `DeliveryOutbox`, `IdGenerator`, `Clock`, `AssignmentPolicy`.
- **Транзакция:** Одна UoW: истечение просроченного pending offer, выбор кандидата, сохранение `Delivery` с прочитанной версией и новый offer с `DeliveryOffered` в Outbox.
- **Допущение:** Без subject и проверки прав — работа процесса. Конкурентное изменение доставки или курьер, переставший быть доступным, — ошибка, которую повторяет retry policy Activity.

## Acceptance

BDD-сценарии размещаются в `acceptance/advance_assignment.feature` после утверждения acceptance criteria в SDD.

Общий каталог: [Delivery use cases](../../../../../README.md#use-cases).

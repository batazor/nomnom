# Offer Delivery To Courier

## Сценарий

- **Тип:** Command.
- **Actor:** Диспетчер Courier Pool или оператор (`platform#offer_delivery`).
- **Намерение:** Предложить доставку заказа выбранному курьеру — на flow-board заказ перетаскивают из кухни на свободного курьера пула.
- **Результат:** Создан актуальный ограниченный по времени `Delivery Offer` выбранному курьеру; assignment создаёт только принятие offer курьером (Accept Delivery Offer).

## Границы

- **Владелец:** `delivery/slices/delivery`.
- **Домен:** `domains/delivery` — `Delivery` (`ensure_assignable`) и `DeliveryOffer` (`DeliveryOffered`).
- **Взаимодействие:** Шаг — application service Offer Delivery (`make_offer`); правила те же, что у offer процесса назначения. Внутренний Domain Event `DeliveryOffered` пишется в Outbox и не публикуется.

## Реализация

- **Handler:** `mod.rs` — `Input { order_id, courier_id }`, `Output` Offer Delivery, `Handler::execute(subject, input)`.
- **Порты:** `DeliveryAuthorization` (`OfferDeliveryToCourier` над `Platform`), `UnitOfWork`, `DeliveryRepository` (`find_by_order`), `CourierAssignment` (порт слайса `courier`), `DeliveryOfferRepository`, `OffersOfDelivery`, `DeliveryOutbox`, `IdGenerator`, `Clock`, `AssignmentPolicy`, `AssignmentProcess` (после commit).
- **Транзакция:** UoW: offer + Outbox (`DeliveryOffered`); открытый offer той же пары возвращается вместо нового и ничего не пишет.
- **Ошибки:** нет `Delivery` заказа — `DeliveryForOrderNotFound` (`NOT_FOUND`); доставка не ожидает курьера, курьер не `available` или из другого Market — precondition (`FAILED_PRECONDITION`).
- **Допущение:** Offers процесса назначения той же доставки не отзываются: доставку получает тот, кто примет первым, остальные offers отзывает Accept Delivery Offer. Срок offer — `AssignmentPolicy::offer_ttl_seconds`.

## Acceptance

Сценарии — тесты `mod.rs`.

Общий каталог: [Delivery use cases](../../../../README.md#use-cases).

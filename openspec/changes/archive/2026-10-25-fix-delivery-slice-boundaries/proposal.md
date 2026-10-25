# Proposal

## Why

В `delivery` два слайса — `courier` и `delivery` — зависят друг от друга по кругу:

- `delivery` импортирует модель `courier` (`CourierId`, `CourierRepository`, `CourierError`) — это ожидаемое направление: назначение доставки опирается на курьера;
- `courier` импортирует модель `delivery` (`Delivery`, `DeliveryRepository`, `Address`, `State`) в use case Get Courier Current Delivery. Это обратное направление и замыкает цикл.

Кроме того, восемь use cases слайса `delivery` (Find Courier, Offer, Accept Offer, Reassign, Replan, Confirm Delivery, Report Failure, Cancel) напрямую вызывают `mark_busy`, `release` и `ensure_available` агрегата `Courier` и сами сохраняют его. Правило «когда курьер занят» исполняется из чужого слайса. Изменение `Delivery`, offer и `Courier` в одной транзакции требуется нормативно (FR-310 «Атомарность Assignment»), но [Unit of Work](../../../docs/unit-of-work.md) называет изменение нескольких aggregates в одной транзакции осознанным исключением, и это исключение нигде не записано.

## What Changes

- Use case Get Courier Current Delivery переезжает из слайса `courier` в слайс `delivery` (он читает данные `Delivery`); authorization по-прежнему проверяет право курьера, gRPC `GetCourierCurrentDelivery` и поведение FR-304 не меняются. После переноса `courier` не импортирует `delivery`.
- Слайс `courier` получает прикладной порт занятости курьера (`CourierAssignment` или аналог: занять, освободить, проверить доступность) в `courier/applications`. Use cases `delivery` меняют занятость только через него, в той же Unit of Work; прямые вызовы методов агрегата `Courier` и `CourierRepository` из `delivery/applications` убираются. Правила доступности остаются в агрегате `Courier`.
- Исключение «offer, `Delivery` и `Courier` в одной транзакции» записывается в [docs/unit-of-work.md](../../../docs/unit-of-work.md) и README `delivery` с причиной: инвариант «у курьера не больше одной активной доставки» нельзя обеспечить eventual consistency без двойного назначения.

Не входит: изменение поведения сценариев, gRPC-контрактов, хранилища, событий; перенос Find Courier в `courier`.

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `delivery-assignment`: занятость курьера меняется через порт слайса `courier`; слайсы без циклических зависимостей.
- `courier-availability`: Get Courier Current Delivery принадлежит слайсу `delivery` без изменения поведения.

## Impact

- `boundaries/delivery/slices/{courier,delivery}/`, `slices/delivery/di`, `infrastructure/grpc/service.rs` (новый путь use case), тесты, README `delivery`, `docs/unit-of-work.md`.
- Внешние контракты не меняются.

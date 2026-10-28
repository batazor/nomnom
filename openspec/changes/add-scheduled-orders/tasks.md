# Tasks

## 1. controlplane: часовой пояс Market

- [ ] 1.1 `time_zone` в `CreateMarket` и реестре, валидация IANA, migration существующих Market; `timeZone` в ConfigMap `GET /markets`; тесты (`FR-689`, `AC-933`, `AC-934`, `FR-632`, `AC-811`).

## 2. restaurants: окна и резервации

- [x] 2.1 Настройки запланированных заказов (ёмкость, `openingLead`, `closingGrace`), gRPC `SetScheduledOrderSettings` с `restaurant#manage`; конфиг `SCHEDULED_SLOT__*` с валидацией на старте; тесты (`FR-672`, `FR-694`, `AC-894`, `AC-895`, `AC-937`).
- [x] 2.2 Слайс резервации: aggregate `ScheduleSlot`, допустимость окна по расписанию, коллекция MongoDB и индексы, List Scheduled Slots, Reserve/Confirm/Release Scheduled Slot, gRPC; MongoDB testcontainers, включая конкурентную резервацию (`FR-673`–`FR-675`, `FR-695`, `FR-703`, `AC-896`–`AC-903`, `AC-956`, `AC-957`).
- [x] 2.3 `baseline_preparation` в представлении ресторана и `restaurants.proto`.

## 3. restaurants: Restaurant Order

- [x] 3.1 Блок `schedule` в Receive Order Request и Restaurant Order (`FR-683`, `AC-921`).
- [x] 3.2 Accept для запланированного заказа и gRPC `QueueRestaurantOrder` для `service` identity `orders` (`FR-222`, `AC-922`, `AC-923`, `FR-224`, `AC-924`).
- [x] 3.3 List Scheduled Orders по gRPC; класс запланированных в правиле очереди (`FR-684`, `AC-925`, `FR-241`, `AC-281`, `AC-862`, `AC-953`, `AC-954`).
- [x] 3.4 Wait Time Statistics от `queued_at` (`FR-644`, `AC-837`, `AC-926`).
- [x] 3.5 Симулятор кухни: приём запланированных заказов без срока.

## 4. delivery

- [ ] 4.1 Статистика `courier_lead` и `transit` по фактам Delivery, конфиг `DELIVERY_ESTIMATE__*`, gRPC `EstimateDeliveryDuration`; PostgreSQL testcontainers (`FR-676`, `FR-691`, `AC-904`, `AC-905`, `AC-935`).

## 5. orders

- [x] 5.1 Order: окно доставки, `OrderPlaced` с окном, migration PostgreSQL, `orders.proto`.
- [x] 5.2 Place Order с резервацией окна и отказами резервации (`FR-681`, `FR-692`, `AC-912`–`AC-915`).
- [x] 5.3 Cancellation policy с `CANCELLATION__SCHEDULED_CUTOFF` (`FR-462`, `AC-916`).
- [x] 5.4 Use case Plan Schedule: domain-политика, клиенты оценок, конфиг `SCHEDULED_ORDER__*` (`FR-677`, `FR-693`, `AC-906`, `AC-936`).
- [x] 5.5 Workflow: ветка запланированного заказа под versioning, stage `scheduled`, таймеры, контрольная точка, Activities `ConfirmSlotReservation`, `PlanSchedule`, `QueueRestaurantOrder`, компенсация `ReleaseSlotReservation`, отсчёт deadlines; workflow-тесты с time skipping и replay (`FR-415`, `FR-416`, `FR-678`–`FR-680`, `FR-682`, `AC-907`–`AC-911`, `AC-917`–`AC-920`).

## 6. BFF и UI

- [x] 6.1 BFF: клиенты `restaurants` и `orders`, `Restaurant.scheduledSlots`, `PlaceOrderInput.deliveryWindowStart`, `Order.deliveryWindow`, `OrderStatus.SCHEDULED`, problems, kitchen-query запланированных; тесты (`FR-685`, `FR-690`, `AC-927`, `AC-928`).
- [x] 6.2 UI покупателя: выбор окна во времени Market, приоритет и для запланированного, альтернативы при отказе, экран запланированного заказа; i18n, generated types, моки, тесты (`FR-686`, `FR-687`, `AC-929`–`AC-931`).
- [x] 6.3 Флоу-борд: колонка «Запланированные» (`FR-688`, `AC-932`).

## 7. ops и документация

- [ ] 7.1 Values `restaurants`, `orders`, `delivery`; GRPCRoute новых методов; seed ёмкости демо-ресторанов и поясов Market; kind e2e запланированного заказа.
- [ ] 7.2 Правка ADR 5 `orders` (stage `scheduled`, таймеры, резервация в Saga), ADR 0002 `restaurants` (коллекция окон), ADR 41 (пояс Market); `docs/kitchen.md` (запланированные заказы, приём ASAP без ограничений, статистика от `queued_at`), `docs/fsm.md`, `docs/ubiquitous-language.md` (Scheduled Order, Delivery Window, Schedule Slot, Slot Reservation, Planned Ready At), README сервисов.

## 8. Проверка

- [ ] 8.1 Проверки каждого сервиса, `make buf-breaking`, `openspec validate add-scheduled-orders --strict`; в kind прервать незавершённые workflow перед выкаткой worker'а, затем прогон e2e.

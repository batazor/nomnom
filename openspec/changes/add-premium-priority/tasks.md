# Tasks

## 1. restaurants: премиум-опция ресторана

- [ ] 1.1 `Restaurant.premiumFee` в домене и документе, Update Restaurant (задать или убрать доплату), `premium_fee` в `RestaurantView` и `restaurants.proto`; тесты (`FR-202`, `AC-849`–`AC-851`).
- [ ] 1.2 gRPC `UpdateRestaurant` в `service.proto`, адаптер с `restaurant#manage`; тесты (`FR-649`, `AC-852`, `AC-853`).

## 2. restaurants: приоритет и строгая очередь

- [ ] 2.1 `priority` в `ReceiveOrderRequestRequest`, Restaurant Order и `RestaurantOrder` proto; migration `STANDARD` для существующих документов; тесты (`FR-651`, `AC-858`, `AC-859`).
- [ ] 2.2 Правило очереди со старением вместо `FifoKitchenQueueOrdering`, порт к базовому времени ресторана, конфиг `KITCHEN_QUEUE__AGING_FACTOR` (валидация на старте, `.env.example`, values); Get Kitchen Queue с `priority`; тесты (`FR-241`, `AC-281`, `AC-862`).
- [ ] 2.3 Start Next Preparation: выбор головы, `start_request_id` и unique partial index, политика повтора, `KitchenQueueEmpty`; MongoDB testcontainers (`FR-225`, `AC-253`, `AC-254`, `AC-255`, `AC-782`, `AC-783`, `AC-860`, `AC-861`).
- [ ] 2.4 gRPC `StartNextPreparation`, `StartPreparation` — `reserved`; маппинг ошибок; тесты (`FR-482`, `AC-543`, `AC-544`).
- [ ] 2.5 Симулятор кухни на `StartNextPreparation`.

## 3. orders

- [ ] 3.1 Order: `priority`, `premium_fee`, `total` с доплатой; migration PostgreSQL; `orders.proto`.
- [ ] 3.2 Place Order: `PlaceOrderRequest.priority`, доплата из catalog snapshot, `PREMIUM_UNAVAILABLE`; unit и PostgreSQL testcontainers (`FR-650`, `AC-854`–`AC-856`).
- [ ] 3.3 Activity `RequestRestaurantOrder` передаёт `priority` в `ReceiveOrderRequest`; тест Activity (`AC-857`).

## 4. BFF и UI

- [ ] 4.1 BFF: клиенты `restaurants` и `orders`, `Restaurant.premiumFee`, `PlaceOrderInput.priority`, `Order.priority`/`premiumFee`, problem `PREMIUM_UNAVAILABLE`, kitchen-мутация `START_NEXT_PREPARATION`, тесты (`FR-652`, `AC-863`).
- [ ] 4.2 UI покупателя: чекбокс приоритета и итог с доплатой при оформлении, метка на экране заказа; i18n, generated types, моки, тесты (`FR-653`, `FR-654`, `AC-864`–`AC-866`).
- [ ] 4.3 Флоу-борд: колонка очереди в порядке Get Kitchen Queue, кнопка «Взять следующий», метка «Приоритет», сообщения отказов; тесты (`FR-655`, `AC-867`, `AC-868`).

## 5. ops и документация

- [ ] 5.1 Values `restaurants` (`KITCHEN_QUEUE__AGING_FACTOR`); kind e2e `ops/kind/e2e` на `StartNextPreparation`.
- [ ] 5.2 `docs/kitchen.md` (порядок Kitchen Queue, Start Preparation, открытые вопросы), `docs/ubiquitous-language.md` (Premium Priority, Start Next Preparation), README `restaurants`, `orders`, `bff`, `ui` и слайсов.

## 6. Проверка

- [ ] 6.1 Проверки каждого сервиса, `make buf-breaking` (ожидаемо падает на `StartPreparation`), `openspec validate add-premium-priority --strict`; в kind прервать незавершённые workflow перед выкаткой worker'а, затем прогон e2e.

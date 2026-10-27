# Tasks

## 1. restaurants: статистика и уровень

- [ ] 1.1 `restaurant_id` в `RestaurantOrderAccepted`, `RestaurantOrderReady`, `RestaurantOrderCancelled`, `PreparationFailed` (`events.proto`, маппер `restaurant-order-events.ts`), тесты маппера (`FR-656`, `AC-869`).
- [ ] 1.2 Domain-функция расчёта в слайсе `restaurant`: базовое время, текущее ожидание, уровень с гистерезисом; unit-тесты (`FR-645`, `FR-657`, `AC-841`–`AC-843`).
- [ ] 1.3 Блок `waitTime` документа `Restaurant`: атомарные update без `version`, migration индекса не нужна; MongoDB testcontainers (`AC-840`).
- [ ] 1.4 Consumer `restaurant.wait-time` в `restaurants-consumer`: маршруты событий, no-op остальных, смена уровня и `RestaurantWaitLevelChanged` в Outbox одной транзакцией; тесты handler и Kafka integration (`FR-644`, `FR-646`, `AC-837`–`AC-839`, `AC-844`, `AC-845`).
- [ ] 1.5 Конфиг `WAIT_TIME__SAMPLE_SIZE`, `WAIT_TIME__RECENT_WINDOW`, `WAIT_TIME__ELEVATED_FACTOR`, `WAIT_TIME__NORMAL_FACTOR`: валидация на старте, `.env.example`, values `restaurants` и `restaurants-consumer`.

## 2. restaurants: каталог и срок готовности

- [ ] 2.1 `RestaurantView` и `Restaurant` в `restaurants.proto`: `wait_time_elevated`, `expected_wait`; `GetRestaurant`, `ListRestaurants`, gRPC-адаптер, тесты (`FR-204`, `AC-211`).
- [ ] 2.2 Обновление кеша по `RestaurantWaitLevelChanged` (перечитать ресторан, новое поколение страниц Market).
- [ ] 2.3 `WaitTimeReadyTimeEstimator` вместо `FixedPreparationTimeEstimator`, порт к статистике слайса `restaurant`; тесты Accept Order (`FR-609`, `AC-846`).

## 3. BFF и UI

- [ ] 3.1 BFF: клиент `restaurants`, `Restaurant.waitTimeElevated` и `expectedWaitMinutes`, presenter, тесты (`FR-647`, `AC-847`).
- [ ] 3.2 UI: бейдж в списке и на странице ресторана, «≈N мин» при оформлении, i18n, generated types, моки и fixtures, тесты (`FR-648`, `AC-848`).

## 4. Документация

- [ ] 4.1 `docs/kitchen.md` (Kitchen Capacity, открытые вопросы), `docs/ubiquitous-language.md` (Wait Time Statistics, Wait Level), ADR 0003 restaurants (новое событие обновления кеша), README `restaurants`, `bff`, `ui` и слайсов.

## 5. Проверка

- [ ] 5.1 Проверки каждого сервиса, `openspec validate add-kitchen-wait-time --strict`, прогон в kind.

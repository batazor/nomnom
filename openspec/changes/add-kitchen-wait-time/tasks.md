# Tasks

## 1. restaurants: статистика и уровень

- [x] 1.1 `restaurant_id` в `RestaurantOrderAccepted`, `RestaurantOrderReady`, `RestaurantOrderCancelled`, `PreparationFailed` (`events.proto`, маппер `restaurant-order-events.ts`), тесты маппера (`FR-656`, `AC-869`).
- [x] 1.2 Domain-функция расчёта в слайсе `restaurant`: базовое время, текущее ожидание, уровень с гистерезисом; unit-тесты (`FR-645`, `FR-657`, `AC-841`–`AC-843`).
- [x] 1.3 Блок `waitTime` документа `Restaurant`: атомарные update без `version`, `waitTime` — поле read model `VersionedCollection`, сохранение агрегата его не трогает; migration не нужна; MongoDB testcontainers (`AC-840`).
- [x] 1.4 Consumer `restaurant.wait-time` в `restaurants-consumer`: маршруты событий, no-op остальных, смена уровня и `RestaurantWaitLevelChanged` в Outbox одной транзакцией; тесты handler и Kafka integration (`FR-644`, `FR-646`, `AC-837`–`AC-839`, `AC-844`, `AC-845`).
- [x] 1.5 Конфиг `WAIT_TIME__SAMPLE_SIZE`, `WAIT_TIME__RECENT_WINDOW`, `WAIT_TIME__ELEVATED_FACTOR`, `WAIT_TIME__NORMAL_FACTOR`: валидация на старте, `.env.example`, values `restaurants` и `restaurants-consumer`.

## 2. restaurants: каталог и срок готовности

- [x] 2.1 `RestaurantView` и `Restaurant` в `restaurants.proto`: `wait_time_elevated`, `expected_wait`; `GetRestaurant`, `ListRestaurants`, gRPC-адаптер, тесты (`FR-204`, `AC-211`).
- [x] 2.2 Обновление кеша по `RestaurantWaitLevelChanged` (перечитать ресторан, новое поколение страниц Market).
- [x] 2.3 `WaitTimeReadyTimeEstimator` вместо `FixedPreparationTimeEstimator`, порт к статистике слайса `restaurant`; тесты Accept Order (`FR-609`, `AC-846`).

## 3. BFF и UI

- [x] 3.1 BFF: клиент `restaurants`, `Restaurant.waitTime { elevated, expectedWaitMinutes }` (только запросы каталога, иначе `null`), presenter, тесты (`FR-647`, `AC-847`).
- [x] 3.2 UI: бейдж в списке и на странице ресторана, «≈N мин» при оформлении, i18n, generated types, моки и fixtures, тесты (`FR-648`, `AC-848`).

## 4. Документация

- [x] 4.1 `docs/kitchen.md` (Kitchen Capacity, открытые вопросы), `docs/ubiquitous-language.md` (Wait Time Statistics, Wait Level), ADR 0003 restaurants (новое событие обновления кеша), README `restaurants`, `bff`, `ui` и слайсов.

## 5. Проверка

- [x] 5.1 Проверки каждого сервиса, `openspec validate add-kitchen-wait-time --strict`, прогон в kind. Chainsaw: 12 из 13; `workloads-healthy-under-policies` красный из-за перезапусков компонентов кластера (cert-manager, envoy-gateway, cilium-operator, kube-controller-manager, kube-scheduler) после создания политик 2026-10-26 и разового `MongoServerSelectionError` при старте `restaurants-consumer`; повторный старт consumer стабилен.

# Tasks

## 0. Проверка Router

- [ ] 0.1 На закреплённой версии Cosmo Router проверить провайдер Redis: шаблон `{{ args.channel }}` в `channels`, требования композиции к `CourierPosition` (`@key`), доставку события без вызова plugin. Если шаблон не поддерживается — остановиться и пересмотреть design.

## 1. delivery: Virtual City и доставка

- [ ] 1.1 VO Virtual City (перекрёсток ↔ координаты, ближайший перекрёсток с прижатием к рамке), конфиг `TRACKING__CITY_*`, валидация на старте; unit-тесты (`FR-658`, `FR-665`, `AC-870`, `AC-871`).
- [ ] 1.2 Порт `RandomSource`; точки pickup/dropoff и Tracking Channel в `Delivery`, Create Delivery, миграция `0008_delivery_tracking.sql`, репозиторий; тесты (`FR-659`, `AC-872`, `AC-873`).
- [ ] 1.3 Query Get Order Tracking (`order#view`), RPC `GetOrderTracking` в `service.proto`, gRPC-адаптер, GRPCRoute; тесты (`FR-660`, `AC-874`–`AC-876`).

## 2. delivery: поток позиций

- [ ] 2.1 Порты `ActiveTrackingChannel` и `CourierPositionPublisher` слайса `courier`, адаптеры в `infrastructure/tracks.rs` (чтение `deliveries`, Valkey `PUBLISH`), `VALKEY__URL`; testcontainers PostgreSQL и Valkey (`FR-661`, `FR-666`, `AC-877`–`AC-879`).
- [ ] 2.2 Update Courier Location: условная запись по `TRACKING__PERSIST_INTERVAL`, публикация, `UNAVAILABLE` при ошибке публикации; тесты (`FR-303`, `AC-307`, `AC-883`).

## 3. delivery: Simulator

- [ ] 3.1 Выборка работы Simulator с точками доставки; маршрут — случайный кратчайший путь по сетке; позиция по доле времени; unit-тесты (`FR-662`, `AC-882`).
- [ ] 3.2 Тик `SIMULATOR__TRACK_INTERVAL` в `di::SimulatorWorker`: маршрут при Accept и Pickup к запланированным моментам, вызов Update Courier Location от имени курьера, валидация конфига; тесты worker (`FR-667`, `AC-880`, `AC-881`, `AC-890`).

## 4. BFF

- [ ] 4.1 Клиент `delivery` `GetOrderTracking`, `Order.tracking` в plugin, presenter, тесты (`FR-663`, `AC-884`, `AC-886`).
- [ ] 4.2 Events subgraph `courierMoved` с `@edfs__redisSubscribe`, провайдер `redis` `tracks` в `config.yaml` и `config.production.yaml`, `BFF__TRACKS_REDIS_URL`, `npm run compose` (`FR-668`, `AC-885`).

## 5. UI

- [ ] 5.1 `CourierMap` в `features/view-order`: SVG-сетка, маркеры, след, интерполяция через `requestAnimationFrame`, `prefers-reduced-motion`, `role="img"`, i18n (`FR-664`, `FR-669`, `AC-887`).
- [ ] 5.2 Подписка `courierMoved`, перезапрос query при переподключении, закрытие на финальном статусе; generated types, MSW-моки, `mocks/subscriptions.ts`, тесты (`FR-669`, `AC-888`, `AC-889`).

## 6. ops

- [ ] 6.1 Valkey `delivery` в `nomnom-data` (ACL `delivery` publish, `bff` subscribe, `exporter`), Secrets `delivery-valkey` и `bff-tracks-valkey`, NetworkPolicy egress, values `delivery` и `bff` (`TRACKING__*`, `SIMULATOR__TRACK_INTERVAL`, `VALKEY__URL`), `.env`/`config.example.toml`.

## 7. Документация

- [ ] 7.1 ADR 36 (подписки с данными в событии и каналом-капабилити), ADR 43 (Valkey `delivery` как pub/sub), README `delivery` (Virtual City, Tracking Channel, Demo Simulator), `bff`, `ui`, `ops/kind/README.md`, `docs/ubiquitous-language.md` (Virtual City, Tracking Channel).

## 8. Проверка

- [ ] 8.1 Проверки каждого сервиса, `openspec validate add-live-courier-tracking --strict`, прогон в kind с включённым Simulator.

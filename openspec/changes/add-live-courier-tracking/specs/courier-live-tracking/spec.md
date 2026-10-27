# Spec Delta

## ADDED Requirements

### Requirement: Virtual City задаётся конфигом delivery
**ID:** FR-658

`delivery` SHALL описывать пространство доставки как Virtual City: квадратную сетку `TRACKING__CITY_BLOCKS × TRACKING__CITY_BLOCKS` кварталов со стороной `TRACKING__BLOCK_METERS` от юго-западного угла `TRACKING__CITY_ORIGIN_LATITUDE`, `TRACKING__CITY_ORIGIN_LONGITUDE`. Перекрёстки сетки SHALL переводиться в широту и долготу.

#### Scenario: Перекрёсток в координатах
**ID:** AC-870

- **GIVEN** Virtual City с углом `(52.5150, 13.3850)`, 12 кварталами и стороной 150 м
- **WHEN** перекрёсток `(0, 2)` переводится в координаты
- **THEN** долгота равна `13.3850`, а широта больше `52.5150` на `300 / 111320` градуса

### Requirement: Конфиг Virtual City обязателен
**ID:** FR-665

Все параметры `TRACKING__CITY_*` и `TRACKING__BLOCK_METERS` обязательны. Пустое или невалидное значение, `blocks < 2`, неположительный размер квартала или рамка вне диапазонов широты и долготы MUST завершать startup.

#### Scenario: Невалидная сетка
**ID:** AC-871

- **WHEN** `TRACKING__CITY_BLOCKS` равен `1` или пуст
- **THEN** процесс `delivery` завершается ошибкой startup

### Requirement: Доставка получает виртуальные точки и Tracking Channel
**ID:** FR-659

Create Delivery SHALL выбирать два разных случайных перекрёстка Virtual City как точки pickup и dropoff и создавать Tracking Channel — 128 случайных бит в 32 hex-символах — и сохранять их в `Delivery`. Повтор Create Delivery по тому же `order_id` MUST возвращать уже сохранённые точки и канал. Доставка, созданная до этого изменения, остаётся без точек и канала.

#### Scenario: Новая доставка
**ID:** AC-872

- **WHEN** `orders` создаёт доставку заказа
- **THEN** у `Delivery` сохранены разные перекрёстки pickup и dropoff и непустой Tracking Channel

#### Scenario: Повтор создания
**ID:** AC-873

- **WHEN** Create Delivery повторяется с тем же `order_id`
- **THEN** точки и Tracking Channel не меняются

### Requirement: Get Order Tracking выдаёт канал только тому, кто видит заказ
**ID:** FR-660

`delivery` SHALL публиковать query Get Order Tracking по `order_id` с permission `order#view`. Для доставки в `assigned` или `picked_up` с Tracking Channel ответ SHALL содержать канал, параметры Virtual City, точки pickup и dropoff и сохранённую позицию курьера с моментом фиксации. В остальных случаях ответ MUST быть пустым. Без `order#view` MUST возвращаться `PERMISSION_DENIED` без канала.

#### Scenario: Курьер едет
**ID:** AC-874

- **GIVEN** доставка заказа в `assigned`
- **WHEN** покупатель запрашивает Get Order Tracking
- **THEN** ответ содержит Tracking Channel, Virtual City, pickup, dropoff и позицию курьера

#### Scenario: Чужой заказ
**ID:** AC-875

- **WHEN** пользователь без `order#view` запрашивает Get Order Tracking
- **THEN** возвращается `PERMISSION_DENIED` без канала

#### Scenario: Доставка завершена
**ID:** AC-876

- **WHEN** доставка в `delivered`, `failed` или `cancelled`
- **THEN** ответ Get Order Tracking пустой

### Requirement: Позиции курьера идут через Valkey pub/sub без Kafka
**ID:** FR-661

Каждый успешный Update Courier Location курьера с активной доставкой (`assigned` или `picked_up`) с Tracking Channel SHALL публиковать в канал Valkey `delivery.tracks.<channel>` JSON с `__typename` `CourierPosition`, каналом, широтой, долготой и `recordedAt` от clock `delivery`. Без активной доставки публикация MUST NOT выполняться. Integration Event и запись в Outbox на точку MUST NOT создаваться.

#### Scenario: Точка активной доставки
**ID:** AC-877

- **GIVEN** курьер везёт заказ
- **WHEN** курьер передаёт позицию
- **THEN** в канал доставки опубликована точка с этими координатами, а в Outbox ничего не добавлено

#### Scenario: Курьер без доставки
**ID:** AC-878

- **WHEN** курьер без активной доставки передаёт позицию
- **THEN** публикации нет

### Requirement: Отказ публикации позиции
**ID:** FR-666

`VALKEY__URL` обязателен: пустое значение MUST завершать startup. Ошибка публикации точки SHALL возвращать из Update Courier Location `UNAVAILABLE`.

#### Scenario: Valkey недоступен
**ID:** AC-879

- **WHEN** публикация точки не удалась
- **THEN** Update Courier Location возвращает `UNAVAILABLE`

### Requirement: Simulator водит курьера по Virtual City
**ID:** FR-662

Включённый Simulator SHALL для доставки с точками в `assigned` строить случайный кратчайший путь по улицам Virtual City от перекрёстка, ближайшего к сохранённой позиции курьера, до pickup с прибытием к `expected_pickup_at`, а после Pickup — от pickup до dropoff к `expected_delivered_at`. Позиция вне рамки SHALL прижиматься к краю. Доставки без точек Simulator MUST NOT двигать.

#### Scenario: Маршрут кратчайший
**ID:** AC-882

- **WHEN** Simulator строит маршрут между двумя перекрёстками
- **THEN** каждый шаг идёт по улице к цели, а длина маршрута равна манхэттенскому расстоянию между перекрёстками

#### Scenario: Курьер приехал
**ID:** AC-881

- **WHEN** наступил `expected_pickup_at`, а Pickup ещё не подтверждён
- **THEN** Simulator передаёт позицию pickup

### Requirement: Simulator передаёт позиции с интервалом движения
**ID:** FR-667

Каждые `SIMULATOR__TRACK_INTERVAL` Simulator SHALL вызывать Update Courier Location от имени курьера с точкой маршрута по доле прошедшего времени. `SIMULATOR__TRACK_INTERVAL` обязателен: ноль или значение больше `SIMULATOR__INTERVAL` MUST завершать startup.

#### Scenario: Курьер едет в ресторан
**ID:** AC-880

- **GIVEN** Simulator включён, курьер принял offer, `expected_pickup_at` через 2 минуты
- **WHEN** проходит половина этого времени
- **THEN** последняя переданная позиция лежит на улице сетки примерно на середине маршрута к pickup

#### Scenario: Невалидный интервал движения
**ID:** AC-890

- **WHEN** `SIMULATOR__TRACK_INTERVAL` больше `SIMULATOR__INTERVAL`
- **THEN** процесс `delivery` завершается ошибкой startup

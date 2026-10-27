# Spec Delta

## ADDED Requirements

### Requirement: Отслеживание курьера в заказе
**ID:** FR-663

`Order` SHALL иметь поле `tracking: OrderTracking` с `channel: ID!`, `city` (угол, число кварталов, сторона квартала), `pickup`, `dropoff` и `courierPosition` (широта, долгота, `recordedAt`) из Get Order Tracking `delivery`, запрошенного от имени покупателя. Без активной доставки поле MUST быть `null`. MSW-моки SHALL покрывать оба случая.

#### Scenario: Заказ в пути
**ID:** AC-884

- **GIVEN** курьер забрал заказ
- **WHEN** покупатель запрашивает `order(id) { tracking { channel courierPosition { latitude longitude } } }`
- **THEN** ответ содержит канал и позицию курьера

#### Scenario: Курьер ещё не назначен
**ID:** AC-886

- **WHEN** заказ ждёт курьера
- **THEN** `Order.tracking` равно `null`

### Requirement: Подписка на позиции курьера
**ID:** FR-668

Подписка `courierMoved(channel: ID!): CourierPosition!` SHALL читать канал Valkey `delivery.tracks.<channel>` через провайдер Redis в Cosmo Streams и отдавать поля из события без обращения к plugin. Без провайдера Router MUST NOT стартовать. In-process моки подписок UI SHALL покрывать `courierMoved`.

#### Scenario: Точка в подписке
**ID:** AC-885

- **GIVEN** покупатель подписан на `courierMoved(channel)`
- **WHEN** `delivery` публикует точку в канал доставки
- **THEN** подписчик получает `CourierPosition` с её координатами и `recordedAt`

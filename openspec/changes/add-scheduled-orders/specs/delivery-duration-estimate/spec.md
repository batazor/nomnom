# Spec Delta

## ADDED Requirements

### Requirement: delivery ведёт статистику длительностей доставки
**ID:** FR-676

`delivery` SHALL хранить по Market последние `DELIVERY_ESTIMATE__SAMPLE_SIZE` длительностей Create Delivery → Pickup (`courier_lead`) и Pickup → вручение (`transit`), обновляя их consumer'ом собственных фактов Delivery. Повтор события MUST NOT менять статистику, отменённая доставка MUST выбывать без длительности.

#### Scenario: Отменённая доставка
**ID:** AC-935

- **WHEN** consumer получает отмену Delivery после Pickup
- **THEN** длительность `transit` этой доставки не записана

### Requirement: EstimateDeliveryDuration отдаёт оценку доставки
**ID:** FR-691

`EstimateDeliveryDuration(market_id)` SHALL отдавать медианы `courier_lead` и `transit` для `service` identity `orders`; пока выборка не набрана — `DELIVERY_ESTIMATE__BASELINE_COURIER_LEAD` и `DELIVERY_ESTIMATE__BASELINE_TRANSIT`. Пустой или невалидный конфиг `DELIVERY_ESTIMATE__*` MUST останавливать процесс на старте.

#### Scenario: Оценка по выборке
**ID:** AC-904

- **GIVEN** выборка Market набрана, медиана Pickup → вручение 22 минуты
- **WHEN** `orders` запрашивает оценку
- **THEN** `transit` равен 22 минутам

#### Scenario: Выборка не набрана
**ID:** AC-905

- **GIVEN** в Market меньше `SAMPLE_SIZE` завершённых доставок
- **WHEN** `orders` запрашивает оценку
- **THEN** ответ содержит базовые значения из конфига

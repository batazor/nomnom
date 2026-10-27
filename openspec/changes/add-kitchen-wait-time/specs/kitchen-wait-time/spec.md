# Spec Delta

## ADDED Requirements

### Requirement: Статистика ожидания строится по фактам Restaurant Order
**ID:** FR-644

`Restaurant` SHALL хранить последние `WAIT_TIME__SAMPLE_SIZE` длительностей «принят → готов» и момент принятия незавершённых заказов. Consumer фактов Restaurant Order SHALL обновлять их атомарно, не меняя `version` ресторана. Отменённый или неудавшийся заказ MUST выбывать без длительности. Повтор события MUST NOT менять статистику. Cook Pool в ней не участвует.

#### Scenario: Заказ приготовлен
**ID:** AC-837

- **GIVEN** заказ принят в 12:00
- **WHEN** consumer получает `RestaurantOrderReady` этого заказа с моментом 12:18
- **THEN** в статистике ресторана появляется длительность 18 минут, а заказ больше не незавершённый

#### Scenario: Повтор события
**ID:** AC-838

- **WHEN** `RestaurantOrderReady` того же заказа доставлен повторно
- **THEN** статистика не меняется

#### Scenario: Отменённый заказ
**ID:** AC-839

- **WHEN** consumer получает `RestaurantOrderCancelled` незавершённого заказа
- **THEN** заказ удалён из незавершённых, длительность не записана

#### Scenario: Правка ресторана не конфликтует со статистикой
**ID:** AC-840

- **WHEN** consumer обновляет статистику между чтением и сохранением Update Restaurant
- **THEN** Update Restaurant сохраняется без конфликта версии

### Requirement: Факты Restaurant Order несут ресторан
**ID:** FR-656

`RestaurantOrderAccepted`, `RestaurantOrderReady`, `RestaurantOrderCancelled` и `PreparationFailed` SHALL нести `restaurant_id`. Событие без `restaurant_id`, опубликованное до этого изменения, consumer статистики SHALL подтверждать без изменения статистики.

#### Scenario: Событие с рестораном
**ID:** AC-869

- **WHEN** кухня принимает заказ
- **THEN** `RestaurantOrderAccepted` несёт `restaurant_id` этого заказа

### Requirement: Текущее ожидание считается от базового времени ресторана
**ID:** FR-645

Базовое время SHALL быть медианой последних `WAIT_TIME__SAMPLE_SIZE` длительностей, а пока их меньше — `KITCHEN__PREPARATION_TIME`. Текущее ожидание SHALL быть большим из двух величин: медианы длительностей за `WAIT_TIME__RECENT_WINDOW` (без них — базового времени) и возраста самого старого незавершённого заказа.

#### Scenario: Нет истории
**ID:** AC-843

- **GIVEN** у ресторана меньше `WAIT_TIME__SAMPLE_SIZE` длительностей и нет незавершённых заказов
- **WHEN** считается ожидание
- **THEN** базовое и текущее ожидание равны `KITCHEN__PREPARATION_TIME`, уровень `NORMAL`

### Requirement: Уровень ожидания меняется с гистерезисом
**ID:** FR-657

Уровень SHALL становиться `ELEVATED`, когда текущее ожидание больше базового в `WAIT_TIME__ELEVATED_FACTOR` раз, и `NORMAL`, когда оно не больше базового в `WAIT_TIME__NORMAL_FACTOR` раз; между порогами уровень MUST NOT меняться. Пустые или невалидные `WAIT_TIME__*` и `NORMAL_FACTOR ≥ ELEVATED_FACTOR` MUST останавливать процесс на старте.

#### Scenario: Кухня не успевает
**ID:** AC-841

- **GIVEN** базовое время 20 минут, `ELEVATED_FACTOR = 1.5`, уровень `NORMAL`
- **WHEN** самый старый незавершённый заказ ждёт 35 минут и приходит факт этого ресторана
- **THEN** уровень становится `ELEVATED`

#### Scenario: Гистерезис
**ID:** AC-842

- **GIVEN** базовое время 20 минут, `NORMAL_FACTOR = 1.2`, уровень `ELEVATED`
- **WHEN** текущее ожидание снижается до 26 минут
- **THEN** уровень остаётся `ELEVATED`, а при 24 минутах становится `NORMAL`

### Requirement: Смена уровня публикуется фактом
**ID:** FR-646

При смене уровня consumer SHALL в той же транзакции сохранить уровень и записать в Outbox `RestaurantWaitLevelChanged` с `restaurant_id`, `market_id` и `elevated` в `restaurants.v1.restaurant-events`. Без смены уровня событие MUST NOT публиковаться. Кеш каталога SHALL обновляться по `RestaurantWaitLevelChanged` так же, как по `RestaurantOpened`.

#### Scenario: Уровень изменился
**ID:** AC-844

- **WHEN** факт кухни переводит уровень ресторана из `NORMAL` в `ELEVATED`
- **THEN** в Outbox один `RestaurantWaitLevelChanged` с `elevated = true`, и после обработки кеш ресторана и страницы его Market показывают повышенное ожидание

#### Scenario: Уровень не изменился
**ID:** AC-845

- **WHEN** факт кухни не меняет уровень
- **THEN** Outbox не пополняется

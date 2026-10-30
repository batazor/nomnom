# Spec Delta

## ADDED Requirements

### Requirement: Граф слайсов restaurants ацикличен
**ID:** FR-728

Production-код слайсов `restaurants` SHALL импортировать соседние слайсы только в направлениях: `menu → restaurant`, `scheduleslot → restaurant`, `restaurantorder → restaurant, menu`, `cook → restaurantorder`, `simulator → restaurantorder, restaurant`. Слайс `restaurant` MUST NOT импортировать другие слайсы. Разрыв циклов MUST NOT менять поведение, gRPC API, события и схему хранения.

#### Scenario: Слайс restaurantorder не знает о соседях-потребителях
**ID:** AC-992

- **WHEN** проверяются импорты `slices/restaurantorder` и `slices/restaurant`
- **THEN** `restaurantorder` не импортирует `cook` и `simulator`, а `restaurant` не импортирует `menu`

#### Scenario: Simulator видит прежние заказы
**ID:** AC-993

- **GIVEN** есть Restaurant Orders в состояниях из `SIMULATED_STATES` и в других состояниях
- **WHEN** Simulator читает заказы, ожидающие кухню
- **THEN** он получает только заказы из `SIMULATED_STATES` в прежнем порядке и с прежними полями

### Requirement: Направления слайсов restaurants проверяются автоматически
**ID:** FR-729

Стандартная проверка `restaurants` SHALL отвергать импорт соседнего слайса вне разрешённого направления, включая импорты `import type` и код `di` слайса.

#### Scenario: Обратная зависимость
**ID:** AC-994

- **WHEN** в `slices/restaurantorder` добавлен `import type` из `slices/cook`
- **THEN** `npm run architecture` завершается ошибкой правила направлений

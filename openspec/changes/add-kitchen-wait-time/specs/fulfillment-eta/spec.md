# Spec Delta

## MODIFIED Requirements

### Requirement: Срок готовности при принятии
**ID:** FR-609

`AcceptRestaurantOrder` SHALL принимать необязательный `expected_ready_at`; заданный срок MUST быть позже момента принятия, иначе `INVALID_ARGUMENT`. Без него срок SHALL считать Ready Time Estimator как момент принятия плюс текущее ожидание ресторана из статистики ожидания (FR-645). Срок уже принятых заказов MUST NOT пересчитываться при изменении статистики. `RestaurantOrderAccepted` SHALL нести `expected_ready_at` Restaurant Order. Эмулятор кухни SHALL передавать срок, равный запланированному моменту `READY`.

#### Scenario: Кухня называет срок
**ID:** AC-769

- **WHEN** кухня принимает заказ с `expected_ready_at = now + 12m`
- **THEN** Restaurant Order и `RestaurantOrderAccepted` несут этот срок

#### Scenario: Срок по статистике
**ID:** AC-846

- **GIVEN** текущее ожидание ресторана 35 минут
- **WHEN** кухня принимает заказ без `expected_ready_at`
- **THEN** `expected_ready_at` равен моменту принятия плюс 35 минут

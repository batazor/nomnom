# Spec Delta

## ADDED Requirements

### Requirement: Повышенное время ожидания видно до заказа
**ID:** FR-648

Customer UI SHALL показывать бейдж «Повышенное время ожидания» у ресторана с `waitTimeElevated` в списке на `/` и на `/restaurants/:restaurantId`, а при оформлении заказа — оценку «≈N мин» из `expectedWaitMinutes`. Оформление MUST оставаться доступным при любом уровне ожидания. Данные SHALL приходить с каталогом, без polling.

#### Scenario: Ресторан перегружен
**ID:** AC-848

- **WHEN** покупатель открывает ресторан с `waitTimeElevated = true` и `expectedWaitMinutes = 42`
- **THEN** UI показывает бейдж и «≈42 мин» при оформлении, а кнопка оформления доступна

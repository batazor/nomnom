# Spec Delta

## ADDED Requirements

### Requirement: Экран запланированного заказа
**ID:** FR-687

Для заказа с `deliveryWindow` экран SHALL показывать окно доставки в шапке во времени Market и статус «Запланирован» при `status = SCHEDULED`. Бар готовки MUST NOT показываться до старта Preparation.

#### Scenario: Заказ ждёт окна
**ID:** AC-931

- **WHEN** покупатель открывает заказ со `status = SCHEDULED` и окном 19:00–19:15
- **THEN** экран показывает «Запланирован», окно 19:00–19:15 и не показывает бар готовки

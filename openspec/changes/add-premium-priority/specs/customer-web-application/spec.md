# Spec Delta

## ADDED Requirements

### Requirement: Выбор приоритета при оформлении
**ID:** FR-653

Если у ресторана есть `premiumFee`, Customer UI SHALL показывать при оформлении заказа чекбокс «Приоритет — приготовим первым, +N» без оценки времени и сумму заказа с доплатой. Без `premiumFee` опция MUST NOT показываться, а `placeOrder` SHALL отправляться с `priority = STANDARD`.

#### Scenario: Покупатель выбирает приоритет
**ID:** AC-864

- **WHEN** покупатель отмечает приоритет в ресторане с доплатой 99 ₽ и оформляет заказ на 500 ₽
- **THEN** UI показывает итог 599 ₽ и отправляет `placeOrder` с `priority = PREMIUM`

#### Scenario: Ресторан без премиума
**ID:** AC-865

- **WHEN** покупатель оформляет заказ в ресторане без `premiumFee`
- **THEN** чекбокса нет, `placeOrder` отправлен с `priority = STANDARD`

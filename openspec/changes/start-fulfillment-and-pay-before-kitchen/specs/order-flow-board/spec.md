# Spec Delta

## ADDED Requirements

### Requirement: Карточка полного цикла показывает оплату
**ID:** FR-641

Карточка полного цикла заказа на флоу-борде SHALL показывать шаги `RECEIVED → PAID → CONFIRMED → PREPARING → READY_FOR_PICKUP → PICKED_UP → DELIVERED` с исполнителем каждого шага; исполнитель шага `PAID` — «Оплата», а не колонка доски. Чип заказа SHALL показывать локализованный статус «Оплачен» для `PAID`. Обновление карточки MUST идти через subscription `orderChanged` (ADR 36), без polling.

#### Scenario: Заказ оплачен и ждёт кухню
**ID:** AC-835

- **WHEN** выбранный заказ получает `status = PAID` через `orderChanged`
- **THEN** шаги «Оформлен» и «Оплачен» отмечены выполненными, следующий — «Принят кухней» с исполнителем «Кухня»

#### Scenario: Оплата отклонена
**ID:** AC-836

- **WHEN** выбранный заказ завершился со `status = FAILED` и `endReason = PAYMENT_DECLINED`
- **THEN** карточка показывает завершение заказа, все шаги в состоянии `ended`

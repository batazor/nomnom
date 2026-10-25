# Proposal

## Why

Экран заказа показывает историю (milestones), но в ней нет «Ресторан подтвердил заказ»: `orders` переводит `Order` в `confirmed` по событию Order Acceptance ресторана, а момент этого события (`OccurredAt`) отбрасывает. Выдумывать время в BFF нельзя — это была бы недостоверная история заказа. Момент известен источнику, его достаточно сохранить.

## What Changes

- Новый Fulfillment Fact `restaurant_accepted`: Confirm Order записывает его с `OccurredAt` события Order Acceptance в той же транзакции, что и переход `placed → confirmed`. `Order.Confirm` принимает момент; пустой момент — ошибка guard.
- `GetOrder` публикует факт в `fulfillment_facts` без изменения контракта (поле `kind` — строка).
- BFF переводит `restaurant_accepted` в milestone `CONFIRMED`; README BFF больше не говорит, что `CONFIRMED` не публикуется.
- Заказы, подтверждённые до изменения, факта не имеют: их история остаётся без `CONFIRMED`. Миграция данных не делается — исходного момента в хранилище нет.

Не входит: изменение статуса заказа (он уже `CONFIRMED` по состоянию), Integration Events `orders`, схема хранения (таблица `order_facts` общая).

## Capabilities

### New Capabilities

Нет.

### Modified Capabilities

- `order-use-cases`: Confirm Order записывает момент подтверждения.
- `customer-order-resolvers`: история заказа содержит `CONFIRMED`.

## Impact

`boundaries/orders` (domain, confirmorder, тесты, README), `boundaries/bff/plugins/bff/ordertracking` (milestones, тесты), README BFF.

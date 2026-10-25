# Spec Delta

## ADDED Requirements

### Requirement: Каталог, корзина и оформление заказа
**ID:** FR-501

Customer UI SHALL показывать открытые рестораны на `/`, меню ресторана с корзиной на `/restaurants/:restaurantId` и оформлять заказ через `placeOrder` с idempotency key на попытку, переиспользуемым при ручном retry; после оформления SHALL переходить на `/orders/:orderId`. Fixture-preview MUST быть удалён.

#### Scenario: Оформление из корзины
**ID:** AC-571

- **WHEN** покупатель добавляет доступные блюда в корзину, вводит адрес и оформляет заказ
- **THEN** UI отправляет `placeOrder` и открывает экран оформленного заказа

#### Scenario: Позиция закончилась при оформлении
**ID:** AC-572

- **WHEN** `placeOrder` возвращает problem `ORDER_ITEM_UNAVAILABLE` для позиции корзины
- **THEN** UI показывает отказ у этой позиции и не очищает корзину

#### Scenario: Недоступное блюдо не добавляется
**ID:** AC-573

- **WHEN** в меню есть позиция с `available = false`
- **THEN** она показана, но действие добавления в корзину недоступно

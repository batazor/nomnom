# Proposal

## Why

Customer UI сейчас работает только на локальных fixtures, а публичная схема BFF содержит один `bffInfo`. Фичи UI ждут готовности backend (хранилища, Kafka, user identity через BFF), хотя единственная business API boundary UI — публичный GraphQL contract. Если зафиксировать customer-схему заранее и обслуживать её MSW-моками, UI и BFF развиваются параллельно с bounded contexts, а схема становится общим контрактом для обеих сторон.

Первым экраном на этом контракте выбран экран отслеживания заказа: он показывает самую богатую часть backend — Fulfillment Facts, компенсации саги и Cancellation Request — и не требует корзины, оплаты и аутентификации на стороне UI.

## What Changes

- Публичная GraphQL-схема BFF (`boundaries/bff/plugins/bff/src/schema.graphql`) расширяется customer-операциями: список ресторанов, ресторан с меню, Place Order с idempotency key, заказ по id, история заказов покупателя, Cancellation Request.
- Схема описывает только customer-facing представление: состояние заказа задаётся публичным enum `OrderStatus`, а имена FSM, Temporal workflow и gRPC-контрактов в неё не попадают. Бизнес-отказы mutations возвращаются типизированным `problem` в payload, а не GraphQL errors.
- BFF plugin регенерируется под новую схему; resolvers новых полей не реализуются и возвращают ошибку «не реализовано», `bffInfo` продолжает работать.
- UI получает MSW-обработчики всех новых операций с набором сценариев заказа (включая отказ ресторана, неудачную доставку, отмену покупателем и заказ, продвигающийся между опросами), подключённые в development и тестах.
- Добавляется feature `view-order`: route `/orders/:orderId` с прогрессом заказа, составом и суммой, понятными пользователю причинами отмены/неудачи и запросом отмены в окне отмены. Обновление — bounded polling, пока экран виден и заказ не завершён.
- Fixture-preview на `/` остаётся без изменений.

Не входит: реализация resolvers BFF и подключение к bounded contexts, аутентификация и user identity, корзина и checkout UI, оплата, ETA и задержка приготовления (не хранятся в `orders`), карта и геопозиция курьера, subscriptions/SSE, браузерный E2E.

## Capabilities

### New Capabilities

- `customer-graphql-contract`: публичная customer GraphQL-схема BFF и MSW-моки, обслуживающие её в development и тестах.
- `order-tracking-ui`: экран отслеживания заказа покупателя в customer UI.

### Modified Capabilities

Нет.

## Impact

- `boundaries/bff/plugins/bff/src/schema.graphql`, `generated/` и `README.md` BFF.
- `boundaries/ui/src/features/view-order/`, `src/shared/graphql/mocks/`, `src/shared/graphql/generated/`, router и i18n messages; `boundaries/ui/README.md`, `docs/ui.md`.
- Bounded contexts, их gRPC-контракты, Helm charts и kind не меняются. Новые runtime dependencies не требуются.

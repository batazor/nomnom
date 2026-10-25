# Tasks

## 1. Customer-схема BFF

- [x] 1.1 Расширить `boundaries/bff/plugins/bff/src/schema.graphql` операциями и типами из design (решения 1–5) с описаниями полей, включая формат RFC 3339 и десятичную строку `Money.amount`; проверить отсутствие имён FSM, Temporal и gRPC в схеме (`FR-450`, `FR-451`, `FR-452`, `FR-453`, `FR-461`).
- [x] 1.2 Регенерировать plugin (`npm run generate`), оставить `UnimplementedBffServiceServer`; добавить в `main_test.go` проверку, что `bffInfo` работает, а `order` возвращает `Unimplemented`; проверить `npm run check` в `boundaries/bff` (`FR-454`, `AC-492`).
- [x] 1.3 Описать customer-операции и статус их реализации в `boundaries/bff/README.md`; проверить локальные Markdown links.

## 2. MSW-моки

- [x] 2.1 Перегенерировать UI types (`npm run codegen`) и добавить typed fixtures ресторанов, меню и восьми сценариев заказа; проверить typecheck (`AC-486`).
- [x] 2.2 Добавить MSW-обработчики всех customer-операций: `null` для неизвестного заказа, продвижение `order-live`, идемпотентные `placeOrder` и `requestOrderCancellation`, problems для недоступной позиции и закрытого окна; проверить unit-тестами (`AC-487`, `AC-490`, `AC-491`, `AC-494`, `AC-503`).
- [x] 2.3 Добавить тест соответствия: каждый document из `src/features/**/*.graphql` исполняется против `buildSchema(schema.graphql)` с ответами моков; убедиться, что тест падает на поле вне схемы (`FR-455`, `AC-493`).

## 3. Feature `view-order`

- [x] 3.1 Создать `features/view-order/` с `order.graphql` и mapping `OrderStatus`/`OrderEndReason`/milestone → typed message, icon и шаг прогресса; проверить unit-тестами полноту mapping для всех значений enum (`AC-488`, `AC-489`).
- [x] 3.2 Реализовать экран на route `/orders/:orderId`: статус, прогресс с отметкой назначения курьера, ресторан, состав и итог через `Intl`, адрес, milestones, skeleton, «не найден» и generic recovery UI; проверить component-тестами на MSW (`AC-495`, `AC-496`).
- [x] 3.3 Реализовать сообщения неуспешного завершения по `endReason` без внутренних кодов и обещаний возврата; проверить component-тестами сценариев `order-cancelled`, `order-restaurant-rejected`, `order-delivery-failed` (`AC-497`).
- [x] 3.4 Реализовать блок отмены: показ по `cancellation.availableUntil`, `requestId` на попытку с переиспользованием при ручном retry, состояния loading/requested/problem/transport error без автоматического retry; проверить component-тестами (`AC-498`, `AC-499`).
- [x] 3.5 Реализовать polling 10 с с паузой по `visibilitychange`, остановкой на финальных статусах, backoff до 60 с и ручным обновлением; объявлять смену статуса через `aria-live`; проверить тестами с fake timers (`AC-500`, `AC-501`).

## 4. Качество и документация

- [x] 4.1 Проверить mobile/tablet/desktop, light/dark, reduced motion, keyboard flow и axe на 390, 768 и 1440px; выполнить `design-taste-frontend` lint/inspect и устранить FAIL (`AC-502`).
- [x] 4.2 Обновить `boundaries/ui/README.md` (route, сценарии моков, development-only) и `docs/ui.md` (публичный `OrderStatus`, polling, зависимость `cancellation` от `orders`); проверить Markdown links.
- [x] 4.3 Выполнить `npm run check` и `npm run build` в `boundaries/ui`, `npm run check` в `boundaries/bff`, `openspec validate add-customer-graphql-contract --strict` и `git diff --check`; подтвердить отсутствие новых runtime dependencies и изменений в bounded contexts.

## Workflow follow-up

- Открыть change в `orders` на публикацию окна отмены в `GetOrder`, без которого BFF не заполнит `Order.cancellation`.
- После review реализации архивировать change через OpenSpec archive workflow.

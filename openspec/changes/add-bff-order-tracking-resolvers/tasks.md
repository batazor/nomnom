# Tasks

## 1. Контексты

- [x] 1.1 `orders`: `transportError` ставит `ErrorInfo.reason` из причины `CancellationNotAllowed`; тест сервиса на закрытое окно (`FR-494`, `AC-562`).
- [x] 1.2 `restaurants`: `GetMenu` в `service.proto`, генерация, обработчик поверх Get Menu, тесты на меню и `NOT_FOUND` (`FR-493`, `AC-561`).

## 2. BFF

- [ ] 2.1 `buf.gen.yaml` клиентов `orders` и `restaurants` из workspace, генерация в `plugins/bff/infrastructure/grpc/gen`.
- [ ] 2.2 Слайс `ordertracking`: сценарии, представление по design (решение 2), порты; unit-тесты на status, endReason, milestones, `lineTotal`, окно отмены (`FR-491`, `AC-557`–`AC-559`).
- [ ] 2.3 gRPC-адаптеры `orders` и `restaurants` с interceptor ADR 32, перевод ошибок по design (решение 4); тесты на bufconn-серверах (`FR-490`, `FR-492`, `AC-555`, `AC-560`).
- [ ] 2.4 Resolvers `QueryOrder` и `MutationRequestOrderCancellation` в `src/main.go`, конфигурация `BFF__INTERNAL_GATEWAY_ADDR`, ошибка аутентификации без access token (`AC-556`); Helm values BFF.

## 3. Документация и проверка

- [ ] 3.1 README BFF (статус операций), README UI (экран работает с реальным BFF, нейтральная подпись позиции без названия); UI: подпись для пустого `menuItem.name`.
- [ ] 3.2 `go test ./...` и `npm run check` в BFF, тесты и линтеры `orders` и `restaurants`, `npm run check` в UI, `openspec validate add-bff-order-tracking-resolvers --strict`.

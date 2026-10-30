# Spec Delta

## ADDED Requirements

### Requirement: RPC restaurants принадлежат слайсам
**ID:** FR-731

Обработчик RPC `RestaurantsService` и его mapping SHALL находиться в `slices/<slice>/infrastructure/grpc/` слайса-владельца. Корневой `infrastructure/grpc` SHALL содержать только сборку сервиса, сервер, идентификацию запроса, ошибки, трассировку, proto, generated code и общий mapping и MUST NOT содержать обработчиков RPC. Слайс MUST NOT импортировать `infrastructure/grpc/service.ts`. Перенос MUST NOT менять proto, ответы, коды ошибок и проверки доступа.

#### Scenario: Изменение RPC меню
**ID:** AC-997

- **WHEN** меняется обработчик `AddMenuItem`
- **THEN** правка находится в `slices/menu/infrastructure/grpc/`, а корневой `infrastructure/grpc` не меняется

#### Scenario: Контракт сервиса не изменился
**ID:** AC-998

- **WHEN** после переноса запускаются gRPC integration-тесты и `npm run proto:check`
- **THEN** проверки проходят без изменения proto и ожидаемых ответов

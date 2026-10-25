# Spec Delta

## ADDED Requirements

### Requirement: Контекст аутентифицирует service identity по projected token
**ID:** FR-431

Каждый gRPC-сервер bounded context SHALL принимать `service` subject только из metadata `authorization: Bearer <jwt>`, подпись которого проверена по JWKS кластера, `iss` равен настроенному issuer, `aud` содержит audience контекста, срок действителен, а `sub` есть в allowlist ServiceAccount. Любое нарушение MUST приводить к `UNAUTHENTICATED`.

#### Scenario: Валидный token Activity worker
**ID:** AC-459

- **WHEN** `orders-worker` вызывает `payments` с token audience `nomnom.payments`, подписанным ключом из JWKS
- **THEN** use case получает `Subject{service, orders-worker}`

#### Scenario: Token другого контекста
**ID:** AC-460

- **WHEN** token с audience `nomnom.delivery` предъявлен `payments`
- **THEN** `payments` отвечает `UNAUTHENTICATED`

#### Scenario: Общие тестовые векторы
**ID:** AC-461

- **WHEN** resolver любого из четырёх контекстов проверяет векторы `tests/identity`
- **THEN** принимаются только `valid` и `valid_string_aud`, остальные отклоняются

### Requirement: Без конфигурации identity сервер отклоняет вызовы
**ID:** FR-432

Если issuer, audience или JWKS не заданы, сервер SHALL использовать `RejectingSubjectResolver`. Невалидный JWKS или allowlist MUST завершать startup ошибкой.

#### Scenario: Конфигурация не задана
**ID:** AC-462

- **WHEN** контекст запущен без `IDENTITY__ISSUER`
- **THEN** все вызовы с subject отклоняются `UNAUTHENTICATED`

### Requirement: orders предъявляет token целевого контекста
**ID:** FR-433

gRPC-клиенты `orders` SHALL прикладывать token из `IDENTITY__TOKEN_DIR/<audience>` целевого контекста, перечитывая файл при каждом вызове.

#### Scenario: Ротация token
**ID:** AC-463

- **WHEN** kubelet заменил файл token между двумя вызовами
- **THEN** второй вызов несёт новый token

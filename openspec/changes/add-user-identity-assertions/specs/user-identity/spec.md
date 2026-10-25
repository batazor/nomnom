# Spec Delta

## ADDED Requirements

### Requirement: Контекст принимает user только от разрешённого делегата
**ID:** FR-471

Каждый gRPC-сервер bounded context SHALL принимать `user` subject только из metadata `x-nomnom-user-assertion` и только после того, как service identity вызывающего аутентифицирована по ADR 27 и входит в `IDENTITY__USER_DELEGATES`. Assertion от service вне allowlist или без service token MUST приводить к `UNAUTHENTICATED`.

#### Scenario: BFF передаёт пользователя
**ID:** AC-525

- **WHEN** вызов несёт валидный service token BFF, BFF входит в `IDENTITY__USER_DELEGATES`, и в metadata есть валидный user assertion с `sub = customer-1`
- **THEN** use case получает `Subject{user, customer-1}`

#### Scenario: Assertion от другого workload
**ID:** AC-526

- **WHEN** вызов несёт валидный service token `orders-worker` и тот же валидный user assertion
- **THEN** сервер отвечает `UNAUTHENTICATED`

### Requirement: Проверка user assertion
**ID:** FR-472

Сервер SHALL проверять user assertion: подпись ES256 по JWKS BFF с обязательным `kid`, `iss` равен `IDENTITY__USER_ISSUER`, `aud` содержит audience контекста, `exp`/`nbf` действительны с допуском 30 s, `exp - iat` не больше 60 s, `sub` и `jti` непусты. Любое нарушение MUST приводить к `UNAUTHENTICATED`, а реализации на всех языках MUST давать одинаковый результат на общих векторах `tests/identity`.

#### Scenario: Assertion для другого контекста
**ID:** AC-527

- **WHEN** BFF передаёт в `orders` assertion с `aud = nomnom.payments`
- **THEN** `orders` отвечает `UNAUTHENTICATED`

#### Scenario: Слишком долгоживущий assertion
**ID:** AC-528

- **WHEN** assertion подписан верным ключом, но `exp - iat` равно 10 минутам
- **THEN** сервер отвечает `UNAUTHENTICATED`

### Requirement: Без конфигурации user assertions отклоняются
**ID:** FR-473

Если не заданы `IDENTITY__USER_ISSUER`, `IDENTITY__USER_JWKS_FILE` или список делегатов пуст, сервер SHALL отклонять любой вызов с user assertion `UNAUTHENTICATED` и MUST продолжать аутентифицировать service identity по ADR 27. Неразбираемый JWKS BFF MUST останавливать процесс на старте.

#### Scenario: Контекст без user-конфигурации
**ID:** AC-529

- **WHEN** контекст запущен без `IDENTITY__USER_JWKS_FILE`, и BFF вызывает его с валидными service token и assertion
- **THEN** вызов отклоняется `UNAUTHENTICATED`, а тот же вызов без assertion обрабатывается как `Subject{service, bff}`

### Requirement: BFF подписывает assertion из проверенного access token
**ID:** FR-474

BFF SHALL формировать user assertion только из `sub` access token, проверенного по JWKS identity provider (подпись, `iss`, `aud` BFF, срок; ID token отклоняется), с `iss = nomnom-bff`, audience целевого контекста, сроком жизни 30 s и случайным `jti`. Клиентские заголовки с идентификатором пользователя MUST игнорироваться.

#### Scenario: Запрос с поддельным заголовком
**ID:** AC-530

- **WHEN** клиент присылает валидный access token пользователя `customer-1` и заголовок `x-user-id: customer-2`
- **THEN** assertion для внутреннего вызова содержит `sub = customer-1`

#### Scenario: Невалидный access token
**ID:** AC-531

- **WHEN** access token истёк или подписан неизвестным ключом
- **THEN** BFF отвечает ошибкой аутентификации и не подписывает assertion

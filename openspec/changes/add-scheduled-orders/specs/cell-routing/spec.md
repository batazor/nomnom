# Spec Delta

## ADDED Requirements

### Requirement: Market хранит часовой пояс
**ID:** FR-689

`CreateMarket` SHALL требовать IANA `time_zone`; пустой или неизвестный пояс MUST давать `INVALID_ARGUMENT` без изменения реестра. Повтор с другим поясом для известного `market_id` MUST давать `ALREADY_EXISTS`. Migration реестра SHALL задавать пояс существующим Market явно.

#### Scenario: Market с поясом
**ID:** AC-933

- **WHEN** оператор вызывает `CreateMarket(market_id: "berlin", time_zone: "Europe/Berlin", …)`
- **THEN** Market создан с поясом `Europe/Berlin`

#### Scenario: Неизвестный пояс
**ID:** AC-934

- **WHEN** оператор вызывает `CreateMarket` с `time_zone: "Mars/Olympus"`
- **THEN** ответ `INVALID_ARGUMENT`, реестр не меняется

## MODIFIED Requirements

### Requirement: Список Market на публичном gateway
**ID:** FR-632

Публичный gateway SHALL отвечать на `GET /markets` сам, без вызова control plane и Cell: `200`, `application/json`, `{"markets":[{"id","name","currency","timeZone"}]}` по name, затем по id. Ответ MUST оставаться доступным при недоступном control plane. Символ `%` в названии Market MUST отдаваться без изменений.

#### Scenario: Список для выбора Market
**ID:** AC-811

- **GIVEN** в реестре Market `hamburg` «Hamburg» и `berlin` «Berlin», оба в EUR и `Europe/Berlin`
- **WHEN** браузер запрашивает `GET /markets`
- **THEN** ответ `{"markets":[{"id":"berlin","name":"Berlin","currency":"EUR","timeZone":"Europe/Berlin"},{"id":"hamburg","name":"Hamburg","currency":"EUR","timeZone":"Europe/Berlin"}]}`

#### Scenario: Название с процентом
**ID:** AC-812

- **GIVEN** Market с названием «Berlin 100%»
- **WHEN** браузер запрашивает `GET /markets`
- **THEN** название в ответе — «Berlin 100%»

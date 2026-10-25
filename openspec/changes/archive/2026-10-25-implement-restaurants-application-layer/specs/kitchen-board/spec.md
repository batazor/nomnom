# Spec Delta

## Purpose

Определяет read models kitchen board `restaurants`: Kitchen Queue, заказы в работе кухни и заказы, ожидающие передачи курьеру, для авторизованных сотрудников ресторана.

## ADDED Requirements

### Requirement: Read models кухни доступны только сотрудникам ресторана
**ID:** FR-240

Get Kitchen Queue, List Active Preparations и List Ready Orders SHALL требовать permission subject над рестораном, выводимую из relation `staff`, и MUST возвращать только заказы этого ресторана без изменения состояния.

#### Scenario: Чужой ресторан
**ID:** AC-280

- **WHEN** subject без права над рестораном запрашивает его Kitchen Queue
- **THEN** запрос отклоняется как permission denied

### Requirement: Get Kitchen Queue возвращает заказы в рекомендованном порядке
**ID:** FR-241

Get Kitchen Queue SHALL возвращать заказы ресторана в `QUEUED` с `accepted_at`, `queued_at` и `expected_ready_at`. Порядок MUST определяться заданной политикой очереди, а не самим запросом; Kitchen Queue MUST NOT быть отдельным aggregate.

#### Scenario: Очередь по политике
**ID:** AC-281

- **WHEN** в очереди несколько принятых заказов
- **THEN** возвращаются только заказы в `QUEUED` в порядке, заданном политикой очереди

### Requirement: List Active Preparations показывает заказы в работе
**ID:** FR-242

List Active Preparations SHALL возвращать заказы ресторана в `PREPARING` и `ASSEMBLED` с State, `preparation_started_at` и текущим `expected_ready_at`, включая перенесённое Preparation Delay.

#### Scenario: Готовящиеся и собранные заказы
**ID:** AC-282

- **WHEN** у ресторана есть заказы в очереди, в Preparation, собранные и готовые
- **THEN** возвращаются только готовящиеся и собранные заказы с актуальным `expected_ready_at`

### Requirement: List Ready Orders показывает заказы, готовые к Pickup
**ID:** FR-243

List Ready Orders SHALL возвращать заказы ресторана в `READY` с `ready_at`. Информационные данные курьера MUST NOT влиять на State заказа и не входят в результат до появления projection событий `delivery`.

#### Scenario: Готовые заказы
**ID:** AC-283

- **WHEN** у ресторана есть готовые и готовящиеся заказы
- **THEN** возвращаются только заказы в `READY` с `ready_at`

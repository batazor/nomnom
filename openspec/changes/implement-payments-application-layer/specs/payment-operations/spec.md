# Spec Delta

## Purpose

Определяет наблюдаемое поведение application-слоя `payments`: создание `Payment`, выполнение Authorization, Capture, Cancel Authorization и полного Refund по запросу вызывающей стороны, применение асинхронных результатов provider и чтение состояния с гарантией не более одного финансового эффекта на логическую операцию.

## ADDED Requirements

### Requirement: Create Payment идемпотентен по Order
**ID:** FR-400

`payments` SHALL создавать не более одного `Payment` на `Order`. Повторный запрос для того же `Order` с той же суммой и валютой MUST возвращать уже созданный `Payment`, а запрос с другой суммой или валютой MUST отклоняться без изменения данных. Create Payment MUST NOT обращаться к payment provider и не публикует бизнес-факт.

#### Scenario: Создание платёжного намерения
**ID:** AC-400

- **WHEN** fulfillment process запрашивает Create Payment для `Order` без существующего `Payment` с положительной точной суммой
- **THEN** создаётся `Payment` в состоянии `pending` и возвращается его идентификатор

#### Scenario: Повторный Create Payment
**ID:** AC-401

- **WHEN** Create Payment повторяется для того же `Order` с той же суммой и валютой
- **THEN** возвращается идентификатор ранее созданного `Payment`, второй `Payment` не создаётся

#### Scenario: Конфликтующий Create Payment
**ID:** AC-402

- **WHEN** Create Payment запрашивается для `Order`, у которого уже есть `Payment` с другой суммой или валютой
- **THEN** запрос отклоняется ошибкой `Payment` already exists, существующий `Payment` не изменяется

#### Scenario: Некорректная сумма
**ID:** AC-403

- **WHEN** сумма нулевая, отрицательная, не является точным десятичным значением или валюта не является кодом ISO 4217
- **THEN** запрос отклоняется ошибкой invalid payment request и `Payment` не создаётся

### Requirement: Финансовая операция требует idempotency key
**ID:** FR-401

Authorize Payment, Capture Payment, Cancel Authorization и Refund Payment SHALL принимать обязательный непустой idempotency key, сформированный вызывающей стороной. `payments` MUST фиксировать `Payment Operation` с этим ключом до обращения к provider и MUST передавать provider тот же ключ.

#### Scenario: Успешная Authorization
**ID:** AC-404

- **WHEN** Authorize Payment выполняется для `Payment` в состоянии `pending` и provider подтверждает Authorization
- **THEN** `Payment` переходит в `authorized`, `Payment Operation` фиксируется как успешная, а факт Authorization записывается для публикации

#### Scenario: Отказ в Authorization
**ID:** AC-405

- **WHEN** provider отклоняет Authorization
- **THEN** `Payment` переходит в `failed`, `Payment Operation` сохраняет отказ с причиной, а факт отказа записывается для публикации

#### Scenario: Пустой idempotency key
**ID:** AC-406

- **WHEN** финансовая операция запрашивается без idempotency key
- **THEN** запрос отклоняется ошибкой invalid payment request без обращения к provider

### Requirement: Повтор операции не создаёт второго финансового эффекта
**ID:** FR-402

Повтор финансовой операции с тем же idempotency key SHALL относиться к той же `Payment Operation`. Завершённая операция MUST возвращать сохранённый результат без повторного обращения к provider. Незавершённая операция MUST повторять обращение к provider только с тем же ключом. Ключ, уже связанный с другим `Payment` или видом операции, MUST отклоняться.

#### Scenario: Повтор после зафиксированного результата
**ID:** AC-407

- **WHEN** Capture Payment повторяется с тем же idempotency key после успешно зафиксированного Capture
- **THEN** возвращается прежний результат, provider не вызывается повторно, состояние и записанные факты не меняются

#### Scenario: Повтор после неизвестного результата
**ID:** AC-408

- **WHEN** предыдущий вызов с тем же idempotency key не зафиксировал результат provider
- **THEN** provider вызывается повторно с тем же ключом, а результат применяется к той же `Payment Operation` один раз

#### Scenario: Ключ другой операции
**ID:** AC-409

- **WHEN** idempotency key уже использован для другого `Payment` или другого вида операции
- **THEN** запрос отклоняется ошибкой idempotency key conflict без обращения к provider

### Requirement: Допустимость операции проверяется до обращения к provider
**ID:** FR-403

`payments` SHALL проверять, что текущее состояние `Payment` допускает запрошенную операцию и что у `Payment` нет другой незавершённой операции, до обращения к provider. Недопустимый запрос MUST отклоняться без вызова provider и без изменения состояния.

#### Scenario: Capture до Authorization
**ID:** AC-410

- **WHEN** Capture Payment запрашивается для `Payment` в состоянии `pending`
- **THEN** запрос отклоняется ошибкой payment state conflict, provider не вызывается

#### Scenario: Другая операция ещё выполняется
**ID:** AC-411

- **WHEN** для `Payment` есть незавершённая операция с другим idempotency key
- **THEN** новая операция отклоняется ошибкой operation in progress, provider не вызывается

### Requirement: Capture списывает полную авторизованную сумму
**ID:** FR-404

Capture Payment SHALL списывать полную сумму `Payment` и допускается только для `authorized`. Отказ provider в Capture MUST сохраняться в `Payment Operation` и записываться для публикации, оставляя `Payment` в `authorized`, чтобы вызывающая сторона могла решить, освобождать ли Authorization.

#### Scenario: Успешный Capture
**ID:** AC-412

- **WHEN** provider подтверждает Capture авторизованного `Payment`
- **THEN** `Payment` переходит в `captured`, а факт Capture записывается для публикации

#### Scenario: Отказ в Capture
**ID:** AC-413

- **WHEN** provider отклоняет Capture
- **THEN** `Payment` остаётся `authorized`, отказ сохраняется в `Payment Operation` и записывается для публикации

### Requirement: Cancel Authorization освобождает авторизованную сумму
**ID:** FR-405

Cancel Authorization SHALL допускаться только для `authorized` и после подтверждения provider MUST возвращать `Payment` в `pending` без `Provider Reference`. Отказ provider MUST сохраняться и записываться для публикации, оставляя `Payment` в `authorized`. Cancel Authorization MUST NOT изменять `Order`.

#### Scenario: Успешная отмена Authorization
**ID:** AC-414

- **WHEN** provider подтверждает отмену Authorization
- **THEN** `Payment` возвращается в `pending`, `Provider Reference` очищается, факт отмены записывается для публикации

#### Scenario: Отказ в отмене Authorization
**ID:** AC-415

- **WHEN** provider отклоняет отмену Authorization
- **THEN** `Payment` остаётся `authorized`, отказ сохраняется и записывается для публикации

### Requirement: Refund возвращает полную списанную сумму
**ID:** FR-406

Refund Payment SHALL возвращать полную сумму и допускается только для `captured`. Partial refund MUST NOT поддерживаться этим поведением. Отказ provider MUST сохраняться и записываться для публикации, оставляя `Payment` в `captured`.

#### Scenario: Успешный Refund
**ID:** AC-416

- **WHEN** provider подтверждает Refund списанного `Payment`
- **THEN** `Payment` переходит в `refunded`, а факт Refund записывается для публикации

#### Scenario: Refund до Capture
**ID:** AC-417

- **WHEN** Refund Payment запрашивается для `Payment`, который не находится в `captured`
- **THEN** запрос отклоняется ошибкой payment state conflict, provider не вызывается

### Requirement: Недоступность provider не меняет бизнес-результат
**ID:** FR-407

Технический отказ provider SHALL возвращаться вызывающей стороне как ошибка provider unavailable. `Payment` и записанные факты MUST оставаться без изменений, а `Payment Operation` MUST оставаться незавершённой, чтобы повтор с тем же idempotency key мог завершить её.

#### Scenario: Provider недоступен
**ID:** AC-418

- **WHEN** обращение к provider завершается техническим отказом
- **THEN** возвращается ошибка provider unavailable, состояние `Payment` не меняется, факт для публикации не записывается

### Requirement: Асинхронно принятая операция ожидает результат
**ID:** FR-408

Если provider принял операцию без окончательного результата, `payments` SHALL вернуть результат `pending` и MUST оставить `Payment` и `Payment Operation` без изменений до применения callback или operation result.

#### Scenario: Provider принял операцию асинхронно
**ID:** AC-419

- **WHEN** provider отвечает, что операция принята и результат придёт позже
- **THEN** вызывающая сторона получает результат `pending`, а `Payment` сохраняет прежнее состояние

### Requirement: Apply Provider Callback дедуплицирует provider message
**ID:** FR-409

Apply Provider Callback SHALL принимать проверенный инфраструктурой callback с идентификатором provider message и ссылкой на известную `Payment Operation`. Повторное получение того же provider message MUST быть no-op. Регистрация message MUST фиксироваться в той же локальной транзакции, что и изменение состояния.

#### Scenario: Callback применяет результат
**ID:** AC-420

- **WHEN** callback сообщает окончательный результат незавершённой `Payment Operation`
- **THEN** результат применяется к `Payment Operation` и `Payment` один раз, а бизнес-факт записывается для публикации

#### Scenario: Повторный callback
**ID:** AC-421

- **WHEN** callback с тем же идентификатором provider message получен повторно
- **THEN** состояние не меняется, новый факт не записывается, результат сообщает о дубликате

#### Scenario: Callback для неизвестной операции
**ID:** AC-422

- **WHEN** callback ссылается на неизвестную `Payment Operation`
- **THEN** callback отклоняется ошибкой operation not found, а provider message не регистрируется как обработанный

### Requirement: Apply Provider Operation Result применяет результат не более одного раза
**ID:** FR-410

Apply Provider Operation Result SHALL сопоставлять полученный вне исходного запроса результат с локальной `Payment Operation`. Совпадающий с уже зафиксированным результат MUST быть no-op, а противоречащий ему MUST отклоняться без изменения состояния.

#### Scenario: Результат незавершённой операции
**ID:** AC-423

- **WHEN** получен окончательный результат незавершённой `Payment Operation`
- **THEN** результат применяется один раз, а бизнес-факт записывается для публикации

#### Scenario: Повтор уже применённого результата
**ID:** AC-424

- **WHEN** получен результат, совпадающий с уже зафиксированным
- **THEN** состояние не меняется и новый факт не записывается

#### Scenario: Противоречащий результат
**ID:** AC-425

- **WHEN** получен результат, противоречащий уже зафиксированному
- **THEN** результат отклоняется ошибкой provider result conflict без изменения состояния

### Requirement: Authorization выполняется до доменной операции
**ID:** FR-411

Каждый use case, вызываемый оркестратором, оператором или пользовательским сценарием, SHALL проверять permission subject над `Payment` до загрузки состояния и обращения к provider. Отказ, ошибка или недоступность authorization MUST приводить к отклонению запроса без изменения данных (fail closed).

#### Scenario: Permission отклонён
**ID:** AC-426

- **WHEN** subject не имеет permission на запрошенную операцию
- **THEN** запрос отклоняется ошибкой permission denied, provider не вызывается, состояние не меняется

#### Scenario: Authorization недоступна
**ID:** AC-427

- **WHEN** проверка permission завершается технической ошибкой
- **THEN** запрос отклоняется, provider не вызывается, состояние не меняется

### Requirement: Get Payment Status не раскрывает provider data
**ID:** FR-412

Get Payment Status SHALL возвращать идентификатор, `Order`, сумму, валюту и состояние `Payment` без изменения данных. `Provider Reference` и иные provider data MUST NOT возвращаться.

#### Scenario: Чтение состояния
**ID:** AC-428

- **WHEN** авторизованный subject запрашивает состояние существующего `Payment`
- **THEN** возвращается текущее состояние без `Provider Reference`

#### Scenario: Неизвестный Payment
**ID:** AC-429

- **WHEN** запрошен несуществующий `Payment`
- **THEN** возвращается ошибка payment not found

### Requirement: List Payment Operations возвращает историю операций
**ID:** FR-413

List Payment Operations SHALL возвращать попытки и результаты операций `Payment` в порядке их начала без изменения данных. `Provider Reference` и иные provider data MUST NOT возвращаться; диагностическое назначение MUST NOT ослаблять authorization.

#### Scenario: История операций
**ID:** AC-430

- **WHEN** авторизованный оператор запрашивает операции `Payment`
- **THEN** возвращаются вид, состояние, сумма, время и причина отказа каждой операции в порядке начала

#### Scenario: Оператор без permission
**ID:** AC-431

- **WHEN** subject без permission запрашивает историю операций
- **THEN** запрос отклоняется ошибкой permission denied

### Requirement: Состояние и бизнес-факты фиксируются атомарно
**ID:** FR-414

Изменение `Payment`, `Payment Operation`, регистрация provider message и записи бизнес-фактов для публикации SHALL фиксироваться в одной локальной транзакции. Сбой любой из этих записей MUST откатывать все изменения транзакции. Provider call MUST NOT выполняться внутри локальной транзакции.

#### Scenario: Сбой записи факта
**ID:** AC-432

- **WHEN** запись бизнес-факта для публикации завершается ошибкой после применения результата provider
- **THEN** изменения `Payment` и `Payment Operation` этой транзакции не фиксируются, а повтор с тем же idempotency key может завершить операцию

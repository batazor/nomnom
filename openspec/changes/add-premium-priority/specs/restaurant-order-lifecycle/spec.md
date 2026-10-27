# Spec Delta

## ADDED Requirements

### Requirement: Restaurant Order хранит приоритет
**ID:** FR-651

Receive Order Request SHALL требовать `priority` (`STANDARD` или `PREMIUM`) и сохранять его в Restaurant Order; отсутствующее или неизвестное значение MUST отклоняться как `INVALID_ARGUMENT`. Приоритет MUST NOT быть State FSM и MUST NOT меняться после создания заказа.

#### Scenario: Премиум-заказ на кухне
**ID:** AC-858

- **WHEN** `ReceiveOrderRequest` приходит с `priority = PREMIUM`
- **THEN** Restaurant Order создан с `priority = PREMIUM`

#### Scenario: Без приоритета
**ID:** AC-859

- **WHEN** `ReceiveOrderRequest` приходит без `priority`
- **THEN** вызов отклоняется `INVALID_ARGUMENT`, Restaurant Order не создан

## MODIFIED Requirements

### Requirement: Start Preparation начинает приготовление заказа из очереди
**ID:** FR-225

Start Next Preparation SHALL принимать `restaurant_id` и обязательный `request_id`, выбирать голову Kitchen Queue ресторана по правилу очереди (FR-241), переводить её из `QUEUED` в `PREPARING` с `preparation_started_at`, `cook_id` и `request_id` и MUST атомарно записывать `PreparationStarted`. Кухня MUST NOT выбирать заказ сама. Cook SHALL выбираться автоматически: свободный Cook ресторана на линии с самым ранним последним назначением, никогда не назначенные — первыми. Без свободного Cook команда MUST отклоняться как `NoCookAvailable`; заказ остаётся в `QUEUED`, Outbox не меняется. Пустая очередь SHALL давать пустой результат без ошибки: ничего не начато. Повтор с тем же `request_id` MUST NOT начинать другой заказ; его поведение MUST задаваться явной политикой composition root: успешный no-op с начатым заказом без бизнес-факта и без нового назначения Cook либо отказ как already in state.

#### Scenario: Кухня начинает приготовление
**ID:** AC-253

- **WHEN** сотрудник кухни берёт следующий заказ, и на линии есть свободный Cook
- **THEN** голова очереди в `PREPARING` с `cook_id` этого Cook и в Outbox записан `PreparationStarted`

#### Scenario: Нет свободного повара
**ID:** AC-782

- **GIVEN** все Cook на линии заняты или линия пуста
- **WHEN** сотрудник кухни берёт следующий заказ
- **THEN** операция отклоняется как `NoCookAvailable`, заказ в `QUEUED`, Outbox не меняется

#### Scenario: Выбор повара
**ID:** AC-783

- **GIVEN** на линии свободны Cook «Айгуль», назначенная 10 минут назад, и «Олег», назначенный 2 минуты назад
- **WHEN** сотрудник кухни берёт следующий заказ
- **THEN** заказу назначена «Айгуль»

#### Scenario: Премиум берётся первым
**ID:** AC-860

- **GIVEN** в очереди обычный заказ с `queued_at` 12:00 и премиум-заказ с `queued_at` 12:05
- **WHEN** сотрудник кухни берёт следующий заказ
- **THEN** начат премиум-заказ

#### Scenario: Пустая очередь
**ID:** AC-861

- **WHEN** сотрудник кухни берёт следующий заказ, а очередь пуста
- **THEN** результат пустой, ни один заказ не начат, Outbox не меняется

#### Scenario: Повтор при политике no-op
**ID:** AC-254

- **WHEN** политика повтора — no-op и команда повторена с тем же `request_id`
- **THEN** возвращается заказ, начатый первой командой, без нового `PreparationStarted`

#### Scenario: Повтор при строгой политике
**ID:** AC-255

- **WHEN** политика повтора — отказ и команда повторена с тем же `request_id`
- **THEN** операция отклоняется как already in state

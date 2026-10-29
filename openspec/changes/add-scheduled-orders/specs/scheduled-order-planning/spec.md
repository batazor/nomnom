# Spec Delta

## ADDED Requirements

### Requirement: План запланированного заказа считается обратным расчётом
**ID:** FR-677

Use case Plan Schedule SHALL вычислять `planned_ready_at = window_start − transit`, `start_at = planned_ready_at − preparation − SCHEDULED_ORDER__PREPARATION_BUFFER`, `courier_request_at = planned_ready_at − courier_lead` и `replan_at = start_at − SCHEDULED_ORDER__REPLAN_LEAD` чистой domain-политикой над UTC-моментами; `preparation` — базовое время ресторана, `transit` и `courier_lead` — оценка `delivery`. Переданный `planned_ready_at` MUST NOT меняться.

#### Scenario: Первичный план
**ID:** AC-906

- **GIVEN** окно 19:00–19:15, `transit` 20 минут, базовое время 25 минут, буфер 5 минут, `courier_lead` 15 минут
- **WHEN** считается план
- **THEN** `planned_ready_at` 18:40, `start_at` 18:10, `courier_request_at` 18:25

### Requirement: Параметры плана обязательны
**ID:** FR-693

`SCHEDULED_ORDER__PREPARATION_BUFFER` и `SCHEDULED_ORDER__REPLAN_LEAD` SHALL задаваться явно; пустое, неразбираемое или неположительное значение MUST останавливать процесс `orders` на старте.

#### Scenario: Буфер не задан
**ID:** AC-936

- **WHEN** процесс `orders` стартует без `SCHEDULED_ORDER__PREPARATION_BUFFER`
- **THEN** загрузка конфигурации завершается ошибкой

### Requirement: Workflow ждёт старта таймерами
**ID:** FR-678

После `RestaurantOrderAccepted` запланированного заказа workflow SHALL находиться в stage `scheduled` и ставить Temporal timers на `replan_at`, `start_at` и `courier_request_at`. В `start_at` он SHALL выполнить `QueueRestaurantOrder`, в `courier_request_at` — `CreateDelivery`. Момент в прошлом MUST означать немедленное выполнение шага. Polling MUST NOT использоваться.

#### Scenario: Старт кухни по таймеру
**ID:** AC-907

- **WHEN** наступает `start_at` запланированного заказа
- **THEN** workflow вызывает `QueueRestaurantOrder`, и заказ появляется в Kitchen Queue

#### Scenario: Момент уже прошёл
**ID:** AC-908

- **GIVEN** кухня приняла заказ позже вычисленного `courier_request_at`
- **WHEN** workflow входит в stage `scheduled`
- **THEN** `CreateDelivery` выполняется сразу, без таймера

### Requirement: Контрольная точка пересчитывает старт
**ID:** FR-679

В `replan_at` workflow SHALL один раз вызвать `PlanSchedule` с прежним `planned_ready_at` и заменить таймеры `start_at` и `courier_request_at` новыми значениями. Изменения загрузки кухни между контрольной точкой и стартом MUST NOT пересчитывать план.

#### Scenario: Кухня загружена сильнее
**ID:** AC-909

- **GIVEN** к `replan_at` базовое время выросло с 25 до 35 минут
- **WHEN** срабатывает контрольная точка
- **THEN** `start_at` переносится на 10 минут раньше, `planned_ready_at` не меняется

#### Scenario: Новый старт уже прошёл
**ID:** AC-910

- **WHEN** пересчитанный `start_at` раньше текущего момента
- **THEN** workflow сразу вызывает `QueueRestaurantOrder`

### Requirement: Курьер вызывается к готовности
**ID:** FR-680

Для запланированного заказа `CreateDelivery` SHALL выполняться в `courier_request_at`, а не после `RestaurantOrderAccepted`. Срока Assignment нет (FR-709).

#### Scenario: Курьер не ищется заранее
**ID:** AC-911

- **WHEN** кухня принимает запланированный заказ за три часа до окна
- **THEN** Delivery не создаётся до `courier_request_at`

# order-relationship-reconciliation Specification

## Purpose
Определяет сверку authorization graph `order#customer` в SpiceDB с business state `orders` в PostgreSQL: двухфазное удаление orphan и stale relationships после grace и восстановление missing relationships.

## Requirements

### Requirement: Orphan relationship удаляется после grace-периода
**ID:** FR-441

Reconciler SHALL удалять `order#customer` relationship, для которой нет `Order` с тем же customer в PostgreSQL, только если она оставалась такой на проходах, разделённых не менее чем grace-периодом. Relationship, ставшая валидной до удаления, MUST сохраняться.

#### Scenario: Связь неудавшегося оформления
**ID:** AC-473

- **WHEN** relationship `order:X#customer` существует, а `Order` X нет на двух проходах с интервалом не меньше grace
- **THEN** reconciler удаляет relationship

#### Scenario: Связь оформляемого заказа
**ID:** AC-474

- **WHEN** relationship найдена без `Order` на первом проходе, а до следующего прохода `Order` закоммичен
- **THEN** relationship не удаляется

### Requirement: Missing relationship восстанавливается
**ID:** FR-442

Для `Order` без relationship своего customer reconciler SHALL выполнить `TOUCH order#customer` и сохранить ZedToken в `authorization_revision`.

#### Scenario: SpiceDB потерял данные
**ID:** AC-475

- **WHEN** `Order` есть в PostgreSQL, а relationship `order#customer` отсутствует
- **THEN** после прохода reconciler relationship существует и customer имеет `view` на заказ

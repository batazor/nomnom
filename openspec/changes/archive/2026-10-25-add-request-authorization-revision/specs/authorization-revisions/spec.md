# Spec Delta

## ADDED Requirements

### Requirement: Revision вызывающего из metadata
**ID:** FR-449

`restaurants` SHALL читать ZedToken из gRPC metadata `authorization-revision` и проверять `platform:nomnom` и ресурсы без сохранённой revision с `at_least_as_fresh` по нему. Сохранённая revision ресурса MUST иметь приоритет; пустое значение или длиннее 1024 символов MUST игнорироваться.

#### Scenario: Оператор сразу после выдачи права
**ID:** AC-485

- **WHEN** оператору выдано `platform#operator`, и он вызывает `RegisterRestaurant` с ZedToken этой записи в `authorization-revision`
- **THEN** регистрация проходит без ожидания репликации

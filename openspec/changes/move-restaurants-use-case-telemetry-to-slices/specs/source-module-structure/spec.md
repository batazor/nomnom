# Spec Delta

## ADDED Requirements

### Requirement: Слайс restaurants владеет списком своих use cases телеметрии
**ID:** FR-730

Каждый слайс `restaurants` SHALL объявлять список своих use cases телеметрии рядом с регистрацией handlers, а корневой `di` MUST NOT перечислять отдельные use cases и SHALL только собирать списки для процесса. Перенос MUST NOT менять имена, типы и набор инструментированных use cases.

#### Scenario: Новый use case слайса
**ID:** AC-995

- **WHEN** в слайс добавляется handler с телеметрией
- **THEN** его регистрация и запись в списке меняются в `di/registrations.ts` слайса, без правки корневого `di`

#### Scenario: Набор use cases сохранён
**ID:** AC-996

- **WHEN** собираются процессы `cmd/restaurants` и `cmd/restaurants-consumer`
- **THEN** каждый инструментирует те же use cases с теми же именами и типами, что и до переноса

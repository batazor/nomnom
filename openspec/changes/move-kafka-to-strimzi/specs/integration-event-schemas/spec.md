## MODIFIED Requirements

### Requirement: Схемы Integration Events регистрируются из buf
**ID:** FR-712

Схемы Integration Events SHALL регистрироваться в Schema Registry только шагом деплоя `schema-registration` из файлов buf-модулей контекстов, скомпилированных компилятором buf: файлы из явного списка `ops/schema-registration/event-files.yaml` вместе с импортированными файлами репозитория. Producers, SMT и consumers MUST NOT регистрировать схемы.

#### Scenario: Повторная регистрация
**ID:** AC-972

- **WHEN** `schema-registration` запускается повторно без изменений `.proto`
- **THEN** новые версии subjects не появляются, утилита завершается успешно

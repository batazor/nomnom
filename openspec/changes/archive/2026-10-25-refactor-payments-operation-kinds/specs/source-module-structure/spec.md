# Spec Delta

## ADDED Requirements

### Requirement: Модули payments по ответственности
**ID:** FR-550

Transport-модуль `payments` SHALL отделять servicer, таблицу ошибок и lifecycle сервера; генератор идентификаторов MUST NOT находиться в gRPC-модуле; протоколы Outbox SHALL быть разделены по ролям потребителей.

#### Scenario: Relay worker
**ID:** AC-640

- **WHEN** relay worker освобождает блокировку при остановке
- **THEN** он зависит только от протокола блокировки, а не от всего хранилища Outbox

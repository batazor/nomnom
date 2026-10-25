# Spec Delta

## ADDED Requirements

### Requirement: Композиция и представление BFF разделены
**ID:** FR-551

BFF SHALL собирать слайсы в одном месте из однократно прочитанной конфигурации с одним соединением на audience; файлы слайсов SHALL содержать только resolvers; перевод прикладных значений в GraphQL-enum SHALL идти по явным таблицам, и неизвестное значение MUST давать ошибку, а не `UNSPECIFIED`.

#### Scenario: Неизвестный статус
**ID:** AC-641

- **WHEN** прикладной слой возвращает статус, которого нет в таблице presenter
- **THEN** resolver отвечает ошибкой, а не заказом со статусом `UNSPECIFIED`

#### Scenario: Ошибка чтения заказа
**ID:** AC-642

- **WHEN** `GetOrder` отвечает `FAILED_PRECONDITION`
- **THEN** BFF не превращает его в отказ отмены

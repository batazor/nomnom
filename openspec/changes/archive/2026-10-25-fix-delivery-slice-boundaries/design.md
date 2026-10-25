# Design

## Context

См. `proposal.md`. Агрегат `Courier` (`slices/courier/domains/courier`) владеет статусами `available`, `busy`, `unavailable` и переходами `ensure_available`, `mark_busy`, `release`, `mark_available`, `mark_unavailable`. Use cases `delivery` загружают `Courier` через `CourierRepository`, вызывают эти методы и сохраняют курьера вместе с `Delivery` и offer в одной Unit of Work (FR-310). Use case `courier/applications/get_courier_current_delivery` объявляет read model `CourierCurrentDeliveryReadModel`, возвращающий поля `Delivery`.

## Goals / Non-Goals

**Goals:** направление зависимостей только `delivery → courier`; изменение занятости — через публичный контракт слайса `courier`; задокументированное исключение UoW.

**Non-Goals:** eventual consistency занятости, изменение поведения и контрактов.

## Decisions

### 1. Get Courier Current Delivery — в слайс `delivery`

Query отвечает на вопрос о доставке и читает её модель, поэтому его владелец — `delivery`. Каталог `get_courier_current_delivery` переезжает в `delivery/applications` вместе с read model и тестами; authorization использует существующий `CourierAuthorization` из `courier` (направление `delivery → courier` допустимо). gRPC-адаптер вызывает handler по новому пути.

Альтернатива — read model в `courier` с собственным DTO без импорта `delivery`. Отклонена: курьерский слайс всё равно знал бы о структуре доставки.

### 2. Порт занятости курьера

В `courier/applications/assignment.rs` (имя уточняется при реализации) объявляется контракт:

```rust
pub trait CourierAssignment {
    fn ensure_available(&self, courier: &CourierId) -> Result<(), CourierAssignmentError>;
    fn occupy(&self, courier: &CourierId) -> Result<(), CourierAssignmentError>;
    fn release(&self, courier: &CourierId) -> Result<(), CourierAssignmentError>;
}
```

Реализация в слайсе `courier` работает с `CourierRepository` в текущей Unit of Work: загружает агрегат, вызывает доменный метод, сохраняет. `delivery`-handlers получают порт вместо `CourierRepository`; их собственный код больше не вызывает методы `Courier`. Ошибки агрегата переводятся в `CourierAssignmentError` с теми же причинами, которые сейчас видят клиенты.

Use cases, которым нужно только прочитать доступность для выбора кандидата (Find Courier), используют `ensure_available` или существующий `CourierLocator`.

### 3. Исключение Unit of Work

В `docs/unit-of-work.md` (раздел о нескольких aggregates) и README `delivery` добавляется запись: Accept Delivery Offer, Reassign Courier и сценарии завершения/отмены меняют offer, `Delivery` и `Courier` одной локальной транзакцией, потому что инвариант «не больше одной активной доставки на курьера» и FR-310 требуют атомарности; конкуренция за `Courier` ограничена optimistic concurrency по `version`.

## Risks / Trade-offs

- [Порт превратится в тонкую обёртку] → он принадлежит слайсу `courier` и является единственной точкой изменения занятости; это и есть цель.
- [Изменение порядка сохранения нарушит FR-310] → тесты атомарности (AC-328) остаются и прогоняются на memory и PostgreSQL store.

## Migration Plan

1. Перенести Get Courier Current Delivery, обновить DI и gRPC.
2. Ввести порт и его реализацию, перевести handlers `delivery` по одному.
3. Документация. Rollback — revert; контракты не менялись.

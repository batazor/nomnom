# Design

## Decisions

1. **Идентификаторы.** `UuidIdGenerator` (UUIDv7) в `infrastructure/ids.rs`; формат строки id не меняет схему (TEXT). Последовательный генератор — только в memory-сборках и тестах.
2. **Локатор.** `StoredCourierLocator` читает последние позиции курьеров в состоянии `available` из `CourierLocationStore`; правило выбора кандидата (`select_candidate`) не меняется. Если позиций нет, кандидатов нет — как и сейчас, но по данным.
3. **Роли хранилища.** Трейты-роли объявляются рядом с потребителями (ADR 6); `PostgresStore` реализует их все, один `Arc` передаётся в каждую роль. Потребитель видит только свою роль.
4. **Replan.** Под одной проверкой `ReplanDelivery` и в одной UoW: `end_assignment` → `select_candidate` → `make_offer`. Handlers Reassign/Find/Offer вызывают те же сервисы после своей авторизации. Поведение и события не меняются.
5. **Снятие назначения.** `release_assignment(delivery, couriers, relationships, Revoke)`; где отзыв не делается, это записывается в README со ссылкой на FR-447 (или исправляется, если это расхождение с FR-447).
6. **Identity.** Модули `jwks`, `service_token`, `user_assertion`, `config`; общие векторы `tests/identity` проходят без изменений.

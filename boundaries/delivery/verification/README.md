# Доказательства ядра Delivery

Инварианты Delivery и Delivery Offer доказываются в [Lean 4](https://lean-lang.org/) для production-кода: [Aeneas](https://github.com/AeneasVerif/aeneas) и Charon извлекают [`fsm/machine.rs`](../slices/delivery/domains/delivery/fsm/machine.rs) и [`offer/machine.rs`](../slices/delivery/domains/delivery/offer/machine.rs) в Lean, а [`lean/Proofs/Delivery.lean`](lean/Proofs/Delivery.lean) и [`lean/Proofs/Offer.lean`](lean/Proofs/Offer.lean) доказывают теоремы на всех входах.

```bash
make verify-lean-setup   # один раз: Aeneas на закреплённом коммите, готовая Mathlib из кеша
make verify-lean         # извлечение и проверка доказательств
```

Нужны `opam`, GNU make (`gmake`) и `elan`. Инструменты ставятся в `AENEAS_HOME`, вне репозитория. Mathlib нужна библиотеке Aeneas (около 4,7 ГБ), сами доказательства её не импортируют. `lean/lean-toolchain` совпадает с версией Lean в Aeneas и меняется вместе с `AENEAS_COMMIT`.

Оба `machine.rs` не используют итераторы и внешние зависимости: `iter().find`, `filter` и `any` в модели стандартной библиотеки Aeneas нет. Сгенерированный `lean/DeliveryCore.lean` в репозиторий не попадает. Сроки оффера и ожидаемое время доказываются на функциях ядра, которые вызывает доменный код.

[`lean/Proofs/Axioms.lean`](lean/Proofs/Axioms.lean) роняет сборку, если теорема в `Proofs` зависит от чего-то кроме `propext`, `Classical.choice` и `Quot.sound`: так ловятся `sorry`, `native_decide` и объявленные `axiom`. Новые теоремы проверяются сами. Ослабленную формулировку теоремы эта проверка не видит, её ловит ревью диффа `Proofs/*.lean`.

В извлечение не входят и проверяются обычными тестами:

- ошибки `transition` и `offer_transition`, адресат оффера, эффекты методов агрегата на поля и события;
- версия агрегата: optimistic concurrency живёт в SQL (`WHERE version = $2`, `version + 1`), а не в Rust, и проверяется интеграционными тестами репозитория.

# Модель протокола назначения

Lean доказывает один шаг автомата, [`quint/assignment.qnt`](quint/assignment.qnt) ([Quint](https://github.com/quint-co/quint)) проверяет чередования шагов: гонки use cases, таймеры и wake Assignment Workflow, grant в SpiceDB и reconciler. Транзакция разбита на чтение и commit, поэтому конкурирующие use cases пересекаются так же, как при `WHERE version = $2`.

```bash
make verify-quint   # нужен Node.js, Quint ставится через npx
```

Модель написана руками, поэтому её сверяют с кодом: [`tests/assignment_model.rs`](../tests/assignment_model.rs) проигрывает её трассы через [Quint Connect](https://github.com/informalsystems/quint-connect) на настоящих use cases и PostgreSQL и после каждого шага сравнивает доставки, офферы, курьеров, grant и пробуждения. Проигрывается `sequentialStep`, по одному use case за раз: use case выполняет транзакцию одним вызовом, поэтому гонки так не проверить, их проверяет симуляция модели. Temporal и SpiceDB в этом тесте заменены записью вызовов. Тест помечен `#[ignore]`: ему нужен `quint` в PATH, и обычный `cargo test` его пропускает. При изменении протокола правят модель, код и драйвер вместе. Симуляция перебирает случайные трассы и ничего не доказывает: инвариант без нарушений значит только, что нарушение не найдено. Apalache для этой модели не используется: на полной модели он не дошёл до 8 шагов, нужных для гонки `accept`, и не поймал даже намеренно сломанную проверку версий за отведённое время. Если симуляция находит нарушение, сначала проверяют, есть ли оно в коде: при исправлении правится и модель.

В модель не входят Kafka и Debezium, геопозиции, Pickup как отдельный шаг, политика выбора курьера и обновление позиции курьера, которое тоже будит поиск (FR-706).

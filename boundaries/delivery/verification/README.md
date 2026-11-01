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

Модель написана руками, и код с ней ничем не связан: при изменении протокола её правят вместе с ним. Симуляция перебирает случайные трассы и ничего не доказывает: инвариант без нарушений значит только, что нарушение не найдено. Если симуляция находит нарушение, сначала проверяют, есть ли оно в коде: при исправлении правится и модель.

В модель не входят Kafka и Debezium, геопозиции, Pickup как отдельный шаг, политика выбора курьера и обновление позиции курьера, которое тоже будит поиск (FR-706).

# Доказательства ядра Delivery

Инварианты Delivery и Delivery Offer доказываются в [Lean 4](https://lean-lang.org/) для production-кода: [Aeneas](https://github.com/AeneasVerif/aeneas) и Charon извлекают [`fsm/machine.rs`](../slices/delivery/domains/delivery/fsm/machine.rs) и [`offer/machine.rs`](../slices/delivery/domains/delivery/offer/machine.rs) в Lean, а [`lean/Proofs/Delivery.lean`](lean/Proofs/Delivery.lean) и [`lean/Proofs/Offer.lean`](lean/Proofs/Offer.lean) доказывают теоремы на всех входах.

```bash
make verify-lean-setup   # один раз: Aeneas на закреплённом коммите, готовая Mathlib из кеша
make verify-lean         # извлечение и проверка доказательств
```

Нужны `opam`, GNU make (`gmake`) и `elan`. Инструменты ставятся в `AENEAS_HOME`, вне репозитория. Mathlib нужна библиотеке Aeneas (около 4,7 ГБ), сами доказательства её не импортируют. `lean/lean-toolchain` совпадает с версией Lean в Aeneas и меняется вместе с `AENEAS_COMMIT`.

Оба `machine.rs` не используют итераторы и внешние зависимости: `iter().find`, `filter` и `any` в модели стандартной библиотеки Aeneas нет. Сгенерированный `lean/DeliveryCore.lean` в репозиторий не попадает. Сроки оффера и ожидаемое время доказываются на функциях ядра, которые вызывает доменный код.

В извлечение не входят и проверяются обычными тестами:

- ошибки `transition` и `offer_transition`, адресат оффера, эффекты методов агрегата на поля и события;
- версия агрегата: optimistic concurrency живёт в SQL (`WHERE version = $2`, `version + 1`), а не в Rust, и проверяется интеграционными тестами репозитория.

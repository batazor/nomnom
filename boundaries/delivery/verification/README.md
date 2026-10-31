# Доказательства FSM оффера

Инварианты Delivery Offer доказываются в [Lean 4](https://lean-lang.org/) для production-кода: [Aeneas](https://github.com/AeneasVerif/aeneas) и Charon извлекают [`offer/machine.rs`](../slices/delivery/domains/delivery/offer/machine.rs) в Lean, а [`lean/Proofs.lean`](lean/Proofs.lean) доказывает теоремы на всех входах.

```bash
make verify-lean-setup   # один раз: Aeneas на закреплённом коммите, готовая Mathlib из кеша
make verify-lean         # извлечение и проверка доказательств
```

Нужны `opam`, GNU make (`gmake`) и `elan`. Инструменты ставятся в `AENEAS_HOME`, вне репозитория. Mathlib нужна библиотеке Aeneas (около 4,7 ГБ), сами доказательства её не импортируют. `lean/lean-toolchain` совпадает с версией Lean в Aeneas и меняется вместе с `AENEAS_COMMIT`.

`machine.rs` не использует итераторы и внешние зависимости: `iter().find`, `filter` и `any` в модели стандартной библиотеки Aeneas нет. Сгенерированный `lean/OfferMachine.lean` в репозиторий не попадает. Ошибки `offer_transition`, срок оффера и адресат проверяются обычными тестами: в извлекаемое ядро они не входят.

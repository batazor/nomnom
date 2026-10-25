# Proposal

## Why

В `payments` реализованы только агрегат `Payment`, `Money` и domain policies; Commands и Queries из `boundaries/payments/README.md` не имеют application handlers. Fulfillment process `orders` уже специфицирован как вызывающий Authorization, Capture, Cancel Authorization и Refund со стабильным idempotency key (`specify-order-fulfillment-orchestration`), поэтому `payments` нужен исполняемый application-слой, гарантирующий не более одного финансового эффекта на логическую операцию.

## What Changes

- Добавить application handlers и dataclass Input/Result для всех девяти use cases `payments`: Create Payment, Authorize Payment, Capture Payment, Cancel Authorization, Refund Payment, Apply Provider Callback, Apply Provider Operation Result, Get Payment Status, List Payment Operations.
- Ввести идемпотентность финансовых операций по idempotency key: повтор с тем же ключом возвращает сохранённый результат без повторного обращения к provider либо повторяет provider call с тем же ключом, если результат ещё не зафиксирован.
- Ввести отдельную дедупликацию provider callbacks по идентификатору provider message и применение результата операции не более одного раза.
- Дополнить доменную модель минимально: запись `Payment Operation` с собственным жизненным циклом, domain events для Authorization, Capture, Cancel Authorization и отказа, контракты repositories, проверка допустимости trigger без изменения состояния.
- Объявить application ports (Unit of Work, Outbox, authorization, payment provider gateway, clock, генератор идентификаторов) и in-memory/fake реализации для unit-тестов; unit-тесты на каждый handler.
- Обновить статус реализации в `boundaries/payments/README.md`.
- Не входит: реальный provider client, gRPC/HTTP transport, хранилище, Kafka, Helm/ops, выбор момента Authorization/Capture (решение оркестратора), partial capture/refund, сроки `PaymentCapturable`/`PaymentRefundable`.

## Capabilities

### New Capabilities

- `payment-operations`: выполнение и наблюдение платёжных операций `payments` по запросу оркестратора, оператора и provider — создание `Payment`, Authorization, Capture, Cancel Authorization, полный Refund, применение асинхронных результатов provider, чтение состояния и истории операций с гарантиями идемпотентности и authorization.

### Modified Capabilities

Нет.

## Impact

- `boundaries/payments/slices/payment/domains/payment/`: новые domain events, `PaymentOperation`, контракты repositories, метод проверки допустимости trigger; существующие доменные тесты обновляются под новые события.
- `boundaries/payments/slices/payment/applications/`: девять use-case packages, общие порты и ошибки слайса.
- `boundaries/payments/pkg/application/`: shared application ports (Unit of Work, clock, subject) и общие application errors.
- `boundaries/payments/tests/`: in-memory/fake adapters и unit-тесты handlers.
- Новых runtime/dev зависимостей нет; `entrypoints/`, Helm и transport contracts не меняются.
- Общие документы (`docs/*`, корневой README, `GLOSSARY-MAP.md`) этим change не изменяются; необходимые правки описаны в design.md как follow-up.

# 2. Temporal для назначения курьера

Date: 2026-10-29

## Status

Accepted

## Context

Назначение курьера шло двумя фоновыми проходами процесса `delivery` по PostgreSQL: Expire Delivery Offers раз в 15 s закрывал pending offers с прошедшим `expires_at`, Dispatch Deliveries раз в 2 s предлагал ожидающие доставки следующим курьерам. Offer живёт 60 s и истекал с опозданием до 15 s; более частые проходы сканируют таблицы и тогда, когда ничего не происходит. Параллельно `orders` ждал назначения 15 минут, перепланировал доставку (`ReplanDelivery`) и эскалировал оператору — логика, дублирующая поиск `delivery`.

Поиск курьера — процесс одной доставки с таймером на каждый offer: ожидание, предложение, ответ или срок, следующий курьер. Temporal уже работает в платформе ([локальный ADR 2 `orders`](../../../orders/docs/adr/0002-use-temporal-to-orchestrate-orders.md)), Temporal Rust SDK вышел в GA 4 сентября 2026 (`temporalio-sdk` 1.1.0).

## Decision

- Назначением каждой `Delivery` управляет Temporal workflow `delivery:<delivery_id>` (Assignment Workflow) на task queue `delivery`. Он живёт до Pickup или закрытия доставки (FR-704) и не хранит её состояние: каждый шаг — Activity над use case Advance Assignment, который читает сохранённую доставку и offers и решает, чего ждать (`Offered`, `NoCandidate`, `Assigned`, `Finished`). Источник истины — PostgreSQL.
- Pending offer ждёт signal или Temporal timer на `expires_at + 1 s`; таймер выполняет Expire Delivery Offer для одного offer (FR-705). Polling нет.
- Signal `wake` с причиной только будит workflow. Use cases передают его после commit, вне Unit of Work: Accept и Reject Delivery Offer, Offer Delivery To Courier, Reassign Courier, Cancel Delivery, Confirm Pickup, Report Delivery Failure; Create Delivery после commit запускает workflow (`RejectDuplicate` + `UseExisting`). Ошибка start или signal — `UNAVAILABLE` при зафиксированной транзакции, повтор вызова передаёт signal заново, даже если состояние не меняется (FR-707). Потерю запуска Create Delivery закрывает retry Activity `orders`.
- Доставка без кандидата ждёт signal без таймеров перепроверки: курьер, ставший Courier Candidate (available с сохранённой позицией — Set Courier Availability, Update Courier Location, освобождение после Confirm Delivery, Report Delivery Failure, Reassign Courier, Cancel Delivery), будит одну доставку — самую старую ожидающую доставку его Market без pending offer, которой его не предлагали (FR-706, query-порт `CandidateDeliveries`).
- Workflow и Activities исполняет отдельный процесс `delivery-worker` (`cmd/delivery-worker`, тот же образ и chart, release `values-worker.yaml`) с конфигурацией только `MANAGEMENT__*`, `SHUTDOWN__*`, `DATABASE__*` и `TEMPORAL__*` (FR-708). Схему мигрирует процесс `delivery`. Процесс `delivery` держит Temporal-клиент для start и signals; `TEMPORAL__HOST_PORT`, `TEMPORAL__NAMESPACE`, `TEMPORAL__CONNECT_TIMEOUT` обязательны обоим, недоступный Temporal — ошибка startup.
- Таймауты и retry policy Activities — константы кода workflow (30 s start-to-close, backoff 1 s × 2 до 60 s), как в `orders`; continue-as-new — по подсказке сервера (`continue_as_new_suggested`). Неизвестная доставка или offer — non-retryable failure, остальные ошибки повторяются.
- SDK подключён без default features и с `vendored-protox`: сборка не требует `protoc`, OpenTelemetry SDK не тянет, `delivery` остаётся на своей версии `opentelemetry`. Spans use cases шагов назначения пишет `delivery-worker` (`nomnom.usecase.duration`); trace context через Temporal headers пока не переносится.
- `orders` ждёт Courier Assignment без срока: replan, решение оператора и `FULFILLMENT__ASSIGNMENT_TIMEOUT` удалены (FR-709), RPC `ReplanDelivery` удалён.

## Consequences

Offer истекает в срок, ожидание без кандидата ничего не опрашивает, новые сроки доставки — таймеры того же workflow. `delivery` зависит от Temporal: без него не создаются доставки и не принимаются ответы курьеров (fail closed, клиент повторяет вызов). Courier Candidate будит одну доставку: если Find Courier пробуждённой доставки выбрал другого курьера, освободившийся курьер ждёт следующего пробуждения своего Market. Workflow SDK исполняет на не-`Send` futures, поэтому worker живёт в собственном потоке с current-thread runtime. Workflows, запущенные до изменения, не переносятся: окружение только для разработки.

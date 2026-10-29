# Tasks

## 1. delivery: Temporal и конфиг

- [x] 1.1 Зависимости `temporalio-sdk`, `temporalio-client`, `temporalio-common`, `temporalio-workflow`, `temporalio-macros` 1.1.0 без default features и с `vendored-protox`: SDK не тянет `opentelemetry` 0.32, `delivery` остаётся на 0.33, сборке не нужен `protoc`.
- [x] 1.2 Конфиг `TEMPORAL__HOST_PORT`, `TEMPORAL__NAMESPACE`, `TEMPORAL__CONNECT_TIMEOUT` для `delivery` и `delivery-worker` (`WorkerConfig`, `load_worker`); таймауты Activities и continue-as-new — в коде workflow; падение на старте при пустом или невалидном значении; `config.example.toml` (`FR-708`, `AC-967`).

## 2. delivery: use cases назначения

- [x] 2.1 Use case Advance Assignment для одной доставки: исходы `offered`, `no_candidate`, `assigned`, `finished`; просроченный pending offer закрывается в той же Unit of Work; PostgreSQL testcontainers (`FR-704`, `FR-711`, `AC-970`).
- [x] 2.2 Expire Delivery Offer для одного offer вместо пачек; принятый, отклонённый, отозванный offer — успех без изменений (`FR-705`, `AC-963`).
- [x] 2.3 Порт `AssignmentProcess` (start, Wake) в `applications/`; start после commit в Create Delivery, в том числе на повторе; Wake после commit в Accept, Reject, Offer Delivery To Courier, Reassign, Cancel Delivery, Confirm Pickup, Report Delivery Failure; ошибка — `UNAVAILABLE`, повтор без изменений передаёт Wake (`FR-306`, `FR-311`, `FR-312`, `FR-707`, `AC-958`, `AC-966`).
- [x] 2.4 Порт `CandidateWake` слайса `courier`, query-порт `CandidateDeliveries` и SQL выбора самой старой подходящей доставки Market; Wake после commit в Set Courier Availability, сохранённой позиции available курьера в Update Courier Location, Confirm Delivery, Report Delivery Failure, Reassign Courier, Cancel Delivery; PostgreSQL testcontainers (`FR-706`, `AC-965`).

## 3. delivery: Temporal-адаптер и worker

- [x] 3.1 `infrastructure/temporal`: workflow `delivery:<delivery_id>` (цикл Advance → timer/Wake → Expire, continue-as-new по подсказке сервера), Activities, адаптер `TemporalAssignment` для client; trace context через headers не переносится (design).
- [x] 3.2 Бинарник `cmd/delivery-worker`, composition root, readiness и остановка в пределах `SHUTDOWN__DRAIN_TIMEOUT`; Temporal client в процессе `delivery`.
- [x] 3.3 Integration-тест workflow (`tests/assignment_workflow.rs`) на Temporal dev server и PostgreSQL из testcontainers: предложение после создания, истечение по таймеру и следующий курьер, ожидание без кандидата и пробуждение, принятие, завершение после Pickup (`AC-959`, `AC-960`, `AC-962`, `AC-964`); Reassign (`AC-961`) — тестом use case.

## 4. delivery: удаление прежнего механизма

- [x] 4.1 Удалить `OfferExpiryWorker`, `DispatchWorker`, конфиг `OFFERS__EXPIRY_*`, `OFFERS__DISPATCH_*`, query-порты `ExpiredDeliveryOffers`, `WaitingDeliveries`, use case Dispatch Deliveries и пакетный Expire Delivery Offers (`FR-582` removed).
- [x] 4.2 Удалить Replan Delivery: use case, `ReplanDelivery` из `service.proto`, gRPC service, `DeliveryPermission::ReplanDelivery` и SpiceDB mapping, observed use cases; перегенерировать stubs `delivery`, `orders`, `bff`; комментарий в `authorization/schema.zed` (`FR-429`, `FR-444`).

## 5. orders: ожидание назначения без срока

- [x] 5.1 Модель fulfillment: удалить `AssignmentTimeout`, `MaxReplans`, `OperatorDecisionTimeout`, `replans`, `replanDue`, `escalated`, `ExpiryEscalation`, шаг `ReplanDelivery`, `OperatorDecision`, stage `waiting_operator`, причину `operator_decision`; табличные тесты модели (`FR-709`, `FR-710`, `AC-968`, `AC-969`).
- [x] 5.2 Temporal-адаптер: signal `OperatorDecision`, Activity и клиент `ReplanDelivery`, логирование эскалации; тесты workflow.
- [x] 5.3 Конфиг: удалить `assignment_timeout`, `max_replans`, `operator_decision_timeout` из `pkg/config/fulfillment.go`, `config.example.yaml`, Helm values.
- [x] 5.4 Ветка запланированного заказа: deadline Assignment больше не отсчитывается от `CreateDelivery`; поправить требование «`CreateDelivery` в `courier_request_at`» в `scheduled-order-planning` (в change `add-scheduled-orders`, если он ещё не архивирован, иначе в `openspec/specs`).

## 6. ops

- [x] 6.1 Helm chart `delivery`: release `delivery-worker` (`values-worker.yaml`), `TEMPORAL__*` в обоих процессах, Valkey только у API, удаление `OFFERS__*`; Dockerfile со вторым бинарником; kind values, skaffold, NetworkPolicy к Temporal, GRPCRoute без `ReplanDelivery`.
- [x] 6.2 Метрики: панелей фоновых проходов не было; источник `expired` в инвентаре — таймер Assignment Workflow. Метрики Temporal SDK не включены (без feature `prometheus`).

## 7. Документация

- [x] 7.1 Локальный ADR 2 `delivery`: Temporal для назначения курьера (workflow на доставку, Wake, signals после commit, SDK без otel); правка ADR 1 `delivery` (query-порты, миграции только у `delivery`).
- [x] 7.2 README `delivery`: таблица use cases, разделы «Истечение Delivery Offers» и «Dispatch Deliveries» → «Assignment Workflow», конфиг, deployable roles, gRPC без `ReplanDelivery`.
- [x] 7.3 README `orders`, `slices/fulfillment`, Temporal-адаптера и ADR 5 `orders`: ожидание курьера без срока, без replan и эскалации.
- [x] 7.4 `docs/ubiquitous-language.md`, `GLOSSARY.md` контекстов: Assignment Workflow, Wake; убрать Replan Delivery и `OperatorDecision`.

## 8. Проверка

- [x] 8.1 `cargo test` `delivery`, `go test ./...` `orders`, `buf lint`.
- [x] 8.2 Деплой в kind (`make kind-deploy`, `make kind-policies`), e2e `order-lifecycle` до вручения; истечение в срок и ожидание без курьеров — integration-тест `tests/assignment_workflow.rs` на Temporal dev server.

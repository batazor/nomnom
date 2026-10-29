# Tasks

## 1. delivery: Temporal и конфиг

- [ ] 1.1 Зависимости `temporalio-sdk`, `temporalio-client` 1.1.0; `opentelemetry`, `opentelemetry_sdk`, `opentelemetry-otlp` 0.32 с комментарием о причине (sdk-rust на 0.32, temporalio/sdk-rust#1715 закрыт без merge); сборка и тесты на новой версии otel.
- [ ] 1.2 Конфиг `TEMPORAL__HOST_PORT`, `TEMPORAL__NAMESPACE`, `TEMPORAL__CONNECT_TIMEOUT` для `delivery` и `delivery-worker`, `ASSIGNMENT_WORKFLOW__CONTINUE_AS_NEW_AFTER`, `ASSIGNMENT_WORKFLOW__ACTIVITY_START_TO_CLOSE` для `delivery-worker`; падение на старте при пустом или невалидном значении; `config.example.toml` (`FR-708`, `AC-967`).

## 2. delivery: use cases назначения

- [ ] 2.1 Use case Advance Assignment для одной доставки: исходы `offered`, `no_candidate`, `assigned`, `finished`; просроченный pending offer закрывается в той же Unit of Work; PostgreSQL testcontainers (`FR-704`, `FR-711`, `AC-970`).
- [ ] 2.2 Expire Delivery Offer для одного offer вместо пачек; принятый, отклонённый, отозванный offer — успех без изменений (`FR-705`, `AC-963`).
- [ ] 2.3 Порт `AssignmentProcess` (start, Wake) в `applications/`; start после commit в Create Delivery, в том числе на повторе; Wake после commit в Accept, Reject, Reassign, Cancel Delivery, Confirm Pickup; ошибка — `UNAVAILABLE`, повтор без изменений передаёт Wake (`FR-306`, `FR-311`, `FR-312`, `FR-707`, `AC-958`, `AC-966`).
- [ ] 2.4 Порт `AssignmentWake` и SQL выбора самой старой подходящей доставки Market; Wake после commit в Set Courier Availability, первой позиции в Update Courier Location, Confirm Delivery, Report Delivery Failure, Reassign Courier, Cancel Delivery; PostgreSQL testcontainers (`FR-706`, `AC-965`).

## 3. delivery: Temporal-адаптер и worker

- [ ] 3.1 `infrastructure/temporal`: workflow `delivery:<delivery_id>` (цикл Advance → timer/Wake → Expire, continue-as-new), Activities, адаптеры портов для client; перенос trace context через headers.
- [ ] 3.2 Бинарник `cmd/delivery-worker`, composition root, readiness и остановка в пределах `SHUTDOWN__DRAIN_TIMEOUT`; Temporal client в процессе `delivery`.
- [ ] 3.3 Тесты workflow в тестовом окружении SDK (feature `testing`): предложение после создания, истечение по таймеру и следующий курьер, ожидание без кандидата и пробуждение, Reassign, завершение после Pickup и Cancel (`AC-959`–`AC-962`, `AC-964`).

## 4. delivery: удаление прежнего механизма

- [ ] 4.1 Удалить `OfferExpiryWorker`, `DispatchWorker`, конфиг `OFFERS__EXPIRY_*`, `OFFERS__DISPATCH_*`, query-порты `ExpiredDeliveryOffers`, `WaitingDeliveries`, use case Dispatch Deliveries и пакетный Expire Delivery Offers (`FR-582` removed).
- [ ] 4.2 Удалить Replan Delivery: use case, `ReplanDelivery` из `service.proto`, gRPC service, `DeliveryPermission::ReplanDelivery` и SpiceDB mapping, observed use cases; перегенерировать stubs `delivery`, `orders`, `bff`; комментарий в `authorization/schema.zed` (`FR-429`, `FR-444`).

## 5. orders: ожидание назначения без срока

- [ ] 5.1 Модель fulfillment: удалить `AssignmentTimeout`, `MaxReplans`, `OperatorDecisionTimeout`, `replans`, `replanDue`, `escalated`, `ExpiryEscalation`, шаг `ReplanDelivery`, `OperatorDecision`, stage `waiting_operator`, причину `operator_decision`; табличные тесты модели (`FR-709`, `FR-710`, `AC-968`, `AC-969`).
- [ ] 5.2 Temporal-адаптер: signal `OperatorDecision`, Activity и клиент `ReplanDelivery`, логирование эскалации; тесты workflow.
- [ ] 5.3 Конфиг: удалить `assignment_timeout`, `max_replans`, `operator_decision_timeout` из `pkg/config/fulfillment.go`, `config.example.yaml`, Helm values.
- [ ] 5.4 Ветка запланированного заказа: deadline Assignment больше не отсчитывается от `CreateDelivery`; поправить требование «`CreateDelivery` в `courier_request_at`» в `scheduled-order-planning` (в change `add-scheduled-orders`, если он ещё не архивирован, иначе в `openspec/specs`).

## 6. ops

- [ ] 6.1 Helm chart `delivery`: Deployment `delivery-worker`, `TEMPORAL__*` и `ASSIGNMENT_WORKFLOW__*` в обоих процессах, удаление `OFFERS__*_INTERVAL/BATCH`; kind values и skaffold.
- [ ] 6.2 Метрики и дашборды: убрать панели фоновых проходов, если есть; worker-метрики Temporal в инвентаре `docs/observability/metric-inventory.md`.

## 7. Документация

- [ ] 7.1 Локальный ADR 2 `delivery`: Temporal для назначения курьера (workflow на доставку, Wake, signals после commit, otel 0.32); правка ADR 1 `delivery` (query-порты).
- [ ] 7.2 README `delivery`: таблица use cases, разделы «Истечение Delivery Offers» и «Dispatch Deliveries» → «Assignment Workflow», конфиг, deployable roles, gRPC без `ReplanDelivery`.
- [ ] 7.3 README `orders`, `slices/fulfillment`, Temporal-адаптера и ADR 5 `orders`: ожидание курьера без срока, без replan и эскалации.
- [ ] 7.4 `docs/ubiquitous-language.md`, `GLOSSARY.md` контекстов: Assignment Workflow, Wake; убрать Replan Delivery и `OperatorDecision`.

## 8. Проверка

- [ ] 8.1 `cargo test` `delivery`, `go test ./...` `orders`, `buf lint`.
- [ ] 8.2 Сброс kind, деплой, e2e заказа до вручения; offer истекает в срок, доставка без курьеров ждёт и получает offer после выхода курьера на линию.

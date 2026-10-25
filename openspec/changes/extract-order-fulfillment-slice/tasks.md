# Tasks

## 1. Доменная модель процесса

- [ ] 1.1 Создать `slices/fulfillment/domains/fulfillment` (Process, Policy, Stage, Outcome, Fact, Step, Effect) без импорта Temporal; перенести правила из `workflow.go` и `signals.go` в чистые методы по design (решение 2), файлы по ответственности (решение 1).
- [ ] 1.2 Табличные unit-тесты модели на все сценарии, которые сейчас покрывает `workflow_test.go`: маршрут, факты, поздние события, replan до `MaxReplans`, эскалация и решение оператора, сроки, Cancellation Request до и после Pickup, компенсации (`FR-483`, `AC-546`, `AC-547`).

## 2. Temporal-адаптер

- [ ] 2.1 Перевести workflow и signal/update handlers на модель; main loop исполняет `Step` как Activities с прежними именами и idempotency key; время передаётся из `workflow.Now`.
- [ ] 2.2 Перенести пакет в `slices/fulfillment/infrastructure/temporal` вместе с activities, client, search attribute и регистрацией; обновить `di/`, wire (`go tool wire ./di`) и импорты.
- [ ] 2.3 Сократить тесты адаптера до интеграции и сквозных сценариев (завершение, отмена, компенсация); проверить имена Activities и idempotency key (`FR-484`, `AC-548`).

## 3. Документация и проверка

- [ ] 3.1 README `orders` (структура слайсов, Order Fulfillment Workflow), README порта `slices/order/applications/fulfillment`, README нового слайса; заметка о replay несовместимости в kind.
- [ ] 3.2 `go test ./...`, `go vet ./...`, `make lint` (кроме известной ошибки gofmt в `pkg/telemetry/telemetry_test.go`), `make test-integration` если доступен Temporal dev server; `openspec validate extract-order-fulfillment-slice --strict`.

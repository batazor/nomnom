# Tasks

## 1. `orders` use-case slices

- [x] 1.1 Добавить общий тип `NotImplementedError` для application layer `orders`, создать отдельные Go slices для всех 10 Commands/Queries из `boundaries/orders/README.md` с `Input`, `Result` и stateless `Handler.Handle`, затем проверить компиляцию `go test ./...`.
- [x] 1.2 Добавить registry smoke test, который явно вызывает все 10 handlers и проверяет каноническое имя use case в `NotImplementedError`; обновить README отметкой о scaffold status и проверить `make check` в `boundaries/orders`.

## 2. `restaurants` use-case slices

- [x] 2.1 Добавить общий `UseCaseNotImplementedError` и отдельные TypeScript slices для всех 17 Commands/Queries из `boundaries/restaurants/README.md` с экспортируемыми `Input`, `Result` и `Handler.execute`.
- [x] 2.2 Добавить parameterized smoke test для всех 17 handlers, обновить README отметкой о scaffold status и проверить `npm test`, `npm run build` и `npm run check` в `boundaries/restaurants`.

## 3. `delivery` use-case slices

- [x] 3.1 Добавить общий тип ошибки и отдельные Rust modules для всех 14 Commands/Queries из `boundaries/delivery/README.md` с `Input`, `Output` и `execute`, экспортировать их из library root без подключения к HTTP binary.
- [x] 3.2 Добавить общий smoke test для всех 14 modules, обновить README отметкой о scaffold status и проверить `make check` в `boundaries/delivery`.

## 4. `payments` use-case slices

- [x] 4.1 Добавить общий `UseCaseNotImplementedError` и отдельные Python packages для всех 9 Commands/Queries из `boundaries/payments/README.md` с dataclass `Input`, `Result` и callable handler.
- [x] 4.2 Добавить table-driven `unittest` для всех 9 handlers, обновить README отметкой о scaffold status и проверить `make check` в `boundaries/payments`.

## 5. Temporal skeleton в `orders`

- [x] 5.1 Добавить официальный Go Temporal SDK в `go.mod/go.sum`, создать `orderfulfillment` application slice с константами workflow/task queue, helper `order:<order_id>`, сериализуемыми input/state и Workflow/Activities stubs без внешних эффектов.
- [x] 5.2 Добавить Temporal test-suite tests для Workflow ID helper и ожидаемого `not implemented` результата workflow/Activities, затем проверить `go test ./...`.
- [x] 5.3 Добавить `cmd/orders-worker` composition root с environment configuration, Temporal client и регистрацией Workflow/Activities; не менять Docker/Helm, документировать локальный compile-only статус и проверить сборку `go build ./cmd/orders-worker` и `make check`.

## 6. Общая проверка

- [x] 6.1 Выполнить `openspec validate add-use-case-slice-stubs --strict`, проверки всех четырёх контекстов и `git diff --check`; подтвердить, что не добавлены transport contracts, Kafka schemas/topics, persistence, provider clients, Helm resources или успешная бизнес-логика.

## Workflow follow-up

- После ревью реализации архивировать change `add-use-case-slice-stubs`.
- Реализовывать каждый use case отдельным SDD change поверх его scaffold.

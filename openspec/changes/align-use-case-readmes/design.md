# Design

## Как отобраны каталоги

Просмотрены все каталоги `applications/*` без README. В список вошли те, где есть handler и комментарий модуля описывает самостоятельную операцию. Остальные каталоги — порты, ошибки, представления и общие шаги — README не получают.

| Каталог | Сценарий | Тип | Actor |
| --- | --- | --- | --- |
| `estimate_delivery_duration` | Оценка `courier_lead` и `transit` по Market (FR-691) | Query | Процесс `orders` |
| `record_delivery_duration` | Запись длительностей по фактам Delivery (FR-676) | Command | Consumer `delivery` |
| `get_order_delivery` | Курьер и живое отслеживание заказа (FR-626, FR-660) | Query | Тот, кому доступен `order:<id>#view` |
| `get_simulator` (`delivery`) | Чтение переключателя Simulator (FR-604) | Query | Оператор платформы |
| `set_simulator_enabled` (`delivery`) | Включение и выключение Simulator (FR-604) | Command | Оператор платформы |
| `planschedule` | Обратный расчёт плана Scheduled Order (FR-677) | Query | Order Fulfillment Workflow |
| `syncorderrelationships` | Приведение связей Order в SpiceDB к состоянию в PostgreSQL (ADR 18) | Command | `orders-consumer` |
| `customersimulator` | Демо-покупатель размещает и отменяет заказы (FR-605) | Command | Фоновый цикл Simulator |
| `switchsimulator` | Чтение и переключение Simulator покупателей (FR-604) | Query и Command | Оператор платформы |

## Формат

Формат совпадает с существующими README, например `delivery/slices/delivery/applications/confirm_pickup/README.md`:

- `## Сценарий`: тип, actor, намерение, результат;
- `## Границы`: владелец, домен, взаимодействие;
- `## Последовательность`: mermaid только для нетривиальной оркестрации, асинхронного обмена или нескольких владельцев.

Диаграмма нужна `syncorderrelationships` (Outbox → consumer → SpiceDB) и `customersimulator` (фоновый цикл через настоящий Place Order). `switchsimulator` содержит два use case, поэтому его README описывает оба.

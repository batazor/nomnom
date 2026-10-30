# Customer Simulator

## Сценарий

- **Тип:** Command.
- **Actor:** Фоновый цикл Simulator от имени демо-покупателя.
- **Намерение:** Создавать демо-нагрузку: размещать заказы в открытых ресторанах и иногда их отменять.
- **Результат:** Пока Simulator включён, после случайной задержки размещается Order, а в 10% случаев после ещё одной задержки запрашивается его отмена, если он всё ещё размещён.

## Границы

- **Владелец:** `orders/slices/simulator`.
- **Домен:** собственного домена нет, заказы создаются настоящими use cases слайса `order`.
- **Взаимодействие:** Вызывает Place Order, Get Order и Request Order Cancellation. Ошибка use case попадает в лог, а следующее действие всё равно планируется. Выключение Simulator сбрасывает расписание.

## Последовательность

```mermaid
sequenceDiagram
    participant Loop as Фоновый цикл
    participant App as Customer Simulator
    participant Switch as Состояние Simulator
    participant Order as Use cases order
    Loop->>App: Tick(now)
    App->>Switch: Enabled?
    alt выключен
        App->>App: сбросить расписание
    else срок заказа наступил
        App->>Order: Place Order
    end
    App->>Order: Get Order и Request Order Cancellation для наступивших отмен
```

## Реализация

Цикл: [`customersimulator.go`](customersimulator.go), unit-тесты: [`customersimulator_test.go`](customersimulator_test.go).

- **Вход:** `Customer.Tick(ctx, now)`.
- **Порты:** `Switch`, `Catalog`, `Random`, `Keys`.
- **Требования:** FR-605 в OpenSpec change `add-demo-simulator`.

Общий каталог: [Orders README](../../../../README.md).

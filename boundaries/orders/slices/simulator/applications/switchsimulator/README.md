# Switch Simulator

Каталог содержит два use case: чтение и переключение Simulator покупателей.

## Сценарий

### Get Simulator

- **Тип:** Query.
- **Actor:** Оператор платформы (`platform#operate_simulator`).
- **Намерение:** Узнать, включён ли Simulator.
- **Результат:** Возвращён сохранённый переключатель; состояние не меняется.

### Set Simulator Enabled

- **Тип:** Command.
- **Actor:** Оператор платформы (`platform#operate_simulator`).
- **Намерение:** Включить или выключить Simulator.
- **Результат:** Переключатель сохранён и возвращён.

## Границы

- **Владелец:** `orders/slices/simulator`.
- **Домен:** собственного домена нет.
- **Взаимодействие:** Читает и пишет сохранённое состояние, которое проверяет фоновый цикл Customer Simulator. Право проверяется до чтения и записи (fail closed).

## Реализация

Handlers: [`switchsimulator.go`](switchsimulator.go).

- **Вход:** `GetQuery{Subject}`, `SetCommand{Subject, Enabled}`.
- **Порты:** `Authorizer`, `State`.
- **Требования:** FR-604 в OpenSpec change `add-demo-simulator`.

Общий каталог: [Orders README](../../../../README.md).

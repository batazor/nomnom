# Register Cell

- **Тип:** Command. **Actor:** оператор платформы (`platform#manage_markets`).
- **Вход:** `Command{Subject, CellID, Router{Namespace, Name, Port}}` — Service Cosmo Router Cell.
- **Порядок:** проверка `manage_markets` в SpiceDB (fail closed) → value objects → `cells.Add`
  (`ON CONFLICT DO NOTHING`); уже зарегистрированная Cell сравнивается `Cell.Repeat` → [публикация
  маршрутов](../publishroutes/README.md).
- **Результат:** `Result{Cell, Created}`; другие значения для известного `cell_id` — `cell.ErrAlreadyRegistered`;
  маршруты не применились — `publishroutes.ErrRoutesUnavailable`, Cell остаётся зарегистрированной.

Handler: [`registercell.go`](registercell.go).

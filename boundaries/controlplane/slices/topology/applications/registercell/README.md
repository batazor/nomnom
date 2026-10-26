# Register Cell

- **Тип:** Command. **Actor:** оператор платформы (`platform#manage_markets`).
- **Вход:** `Command{Subject, CellID, GatewayAddress}`.
- **Порядок:** проверка `manage_markets` в SpiceDB (fail closed) → value objects → `cells.Add`
  (`ON CONFLICT DO NOTHING`); уже зарегистрированная Cell сравнивается `Cell.Repeat`.
- **Результат:** `Result{Cell, Created}`; другие значения для известного `cell_id` — `cell.ErrAlreadyRegistered`.

Handler: [`registercell.go`](registercell.go).

# List Markets

- **Тип:** Query. **Actor:** оператор платформы (`platform#manage_markets`).
- **Результат:** все неархивные Market по `name`, затем `market_id`. Клиенты берут список Market у публичного
  gateway (`GET /markets`), а не у control plane.

Handler: [`listmarkets.go`](listmarkets.go).

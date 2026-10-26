# Get Placement Map

- **Тип:** Query. **Actor:** service identity BFF (`IDENTITY__PLACEMENT_READERS`).
- **Результат:** `Placement{MarketID, CellID, GatewayAddress}` на каждый Market по `market_id` —
  по ней BFF выбирает gateway Cell запроса.

Handler: [`getplacementmap.go`](getplacementmap.go).

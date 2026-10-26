# Publish Routes

- **Тип:** внутренний сценарий control plane; вызывается после `RegisterCell` и `CreateMarket` (в том числе
  повтора) и один раз при старте процесса до readiness.
- **Порядок:** read-only транзакция с transaction-level advisory lock PostgreSQL → снимок реестра (Market с Router своей Cell, по `name`,
  затем `market_id`) → server-side apply ресурсов публичного gateway → конец транзакции снимает lock.
- **Результат:** маршрут каждого Market и список Market применены; ошибка применения — `ErrRoutesUnavailable`
  (`UNAVAILABLE`, `ROUTES_UNAVAILABLE`), запись реестра не откатывается.

Handler: [`publishroutes.go`](publishroutes.go); ресурсы — `infrastructure/gatewayroutes`.

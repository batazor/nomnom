# Archive Market

- **Тип:** Command. **Actor:** оператор платформы (`platform#manage_markets`).
- **Вход:** `market_id`.
- **Результат:** Market помечен архивным (`archived_at`): он пропадает из `ListMarkets`, из списка
  Market публичного gateway (`GET /markets`) и его маршрут `/m/{market_id}` удаляется. Строка
  остаётся, `market_id` больше не создаётся (`CreateMarket` — `ALREADY_EXISTS`, `MARKET_ARCHIVED`).
- **Повтор:** уже архивный Market — успех с `archived = false`. Неизвестный `market_id` — `NOT_FOUND`
  (`MARKET_NOT_FOUND`).
- **После записи** (и после повтора) маршруты публичного gateway публикуются из реестра; если не
  вышло — `UNAVAILABLE` (`ROUTES_UNAVAILABLE`), архив остаётся, повтор публикует снова.
- **Зачем:** прогоны kind e2e создают Market на прогон и архивируют его в cleanup.

Handler: [`archivemarket.go`](archivemarket.go).

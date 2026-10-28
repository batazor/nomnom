# Create Market

- **Тип:** Command. **Actor:** оператор платформы (`platform#manage_markets`).
- **Вход:** `Command{Subject, MarketID, Name, Currency, TimeZone, CellID}`; название хранится обрезанным,
  `TimeZone` — обязательный IANA-пояс (`time.LoadLocation`), пустой или неизвестный — `vo.ErrInvalidValue`.
- **Порядок:** `manage_markets` → value objects → `markets.Add`; внешний ключ на `cells` отклоняет
  незарегистрированную Cell (`market.ErrUnknownCell`); существующий Market сравнивается `Market.Repeat` →
  [публикация маршрутов](../publishroutes/README.md).
- **Результат:** `Result{Market, Created}`; другие значения — `market.ErrAlreadyCreated`; маршруты не
  применились — `publishroutes.ErrRoutesUnavailable`, Market остаётся в реестре, повтор применяет их снова.

Handler: [`createmarket.go`](createmarket.go).

# Design

## Decisions

1. `wiring.go` строит `slices{tracking, catalog, checkout}` из одной конфигурации; ошибки конфигурации возвращаются в `main`, который единственный вызывает `log.Fatalf`.
2. `requireCustomer(ctx) (context.Context, error)` — только аутентификация; доступность слайса проверяет `requireSlice(name, ok)` с именем слайса в сообщении.
3. Presenter: `map[applications.Status]service.OrderStatus` и т. п.; тест перечисляет все прикладные константы.
4. `grpcerr.Info(err) (reason, metadata, ok)`; каждый адаптер переводит ошибки своей операции.
5. Порты объявлены рядом со сценариями; адаптеры реализуют несколько портов.

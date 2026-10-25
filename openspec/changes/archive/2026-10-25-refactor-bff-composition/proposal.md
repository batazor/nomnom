# Proposal

## Why

Ревью SOLID BFF-плагина:

- Каждый `src/<slice>.go` одновременно читает окружение и подключается к gateway (с `log.Fatalf`), держит resolvers и хелперы, которыми пользуются другие слайсы (`verifiedCustomer`, `internalError`, `orderMessage`, `money`, `timestamp` в `ordertracking.go`). `BFF__INTERNAL_GATEWAY_ADDR` читается трижды, к `nomnom.orders` и `nomnom.restaurants` открывается по два соединения.
- `verifiedCustomer(ctx, available)` смешивает аутентификацию и «подключён ли слайс» и всегда отвечает «order operations are not available».
- Значения GraphQL-enum получаются склейкой строк `X_value["PREFIX_"+v]`: неизвестное значение молча становится `UNSPECIFIED`, а прикладные константы обязаны повторять имена схемы.
- `ordersError` обслуживает два RPC: `FAILED_PRECONDITION` на `GetOrder` превратился бы в `CancellationRefused`; разбор `ErrorInfo` продублирован в checkout.
- Порты шире сценариев: `Orders` (GetOrder и RequestCancellation), `Catalog` (список и один ресторан).
- `identity.Identity` объединяет входящую проверку access token и исходящие учётные данные; interceptor принимает конкретный `*AssertionSigner`.

## What Changes

- `src/wiring.go`: конфигурация читается один раз, одно соединение на audience, сборка слайсов; `src/auth.go` (`requireCustomer`), `src/errors.go`, `src/order_presenter.go` (общие mappers); файлы слайсов содержат только resolvers.
- Явные таблицы enum в presenter с тестом полноты; неизвестное значение — ошибка resolver.
- Mapper ошибок на каждую операцию; общий `infrastructure/grpcerr` для `ErrorInfo` и metadata.
- Порты по сценариям: `OrderReader`, `CancellationRequester`, `RestaurantLister`, `RestaurantReader`.
- `identity`: входящая (`AccessTokenVerifier`) и исходящая (`OutgoingCredentials`: service tokens, `Signer`) части; interceptor принимает интерфейс `Signer`.

Не входит: изменение схемы GraphQL и поведения resolvers.

## Capabilities

### Modified Capabilities

- `source-module-structure`: композиция BFF.

## Impact

`boundaries/bff/plugins/bff` (src, слайсы, identity, infrastructure), README BFF.

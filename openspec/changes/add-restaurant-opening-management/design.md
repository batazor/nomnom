# Design

## Context

`OpenRestaurant`/`CloseRestaurant` (`restaurants.v1`) требуют `restaurant#manage` у `user`-subject: вызов только service identity BFF будет отклонён. BFF уже ходит в `orders` от имени покупателя (`PlaceOrder`, `GetOrder`) через `identity.UnaryClientInterceptor`, который ставит user assertion, если в контексте есть `identity.WithUser`. Каталог (`GetRestaurant`, `ListRestaurants`, `GetMenu`) вызывается `identity.WithoutUser`.

## Decisions

1. **Отдельный слайс `restaurantmanagement`.** Сценарии владельца не относятся к каталогу покупателя: у них другой subject и другие отказы. Слайс использует общее соединение `nomnom.restaurants` из `wiring.go` (FR-551); порт `RestaurantOpener` объявлен рядом со сценариями `OpenRestaurant`/`CloseRestaurant`.
2. **От имени пользователя.** Resolver вызывает `requireCustomer` (проверка access token, `identity.WithUser`), контекст доходит до адаптера без `WithoutUser`, поэтому interceptor ставит service token BFF и user assertion (ADR 32). BFF не проверяет владение: authoritative check — `restaurant#manage` в `restaurants` (ADR 18).
3. **Payload переиспользует `Restaurant`.** `acceptingOrders` уже означает `is_open`; отдельное поле `isOpen` дублировало бы его. `menu` в payload — `null`: команды меню не читают. `changed` передаётся как есть, чтобы UI различал «открыт» и «уже был открыт».
4. **Отказы — GraphQL errors, не `problems`.** У открытия нет отказа, который пользователь исправит в форме: несуществующий ресторан, нет права или неверный id. Так же `placeOrder` отвечает error `NOT_FOUND` на несуществующий ресторан. Коды: `NOT_FOUND` → `NOT_FOUND`, `PERMISSION_DENIED` → `PERMISSION_DENIED` (ресторан — публичные данные, скрывать его существование, как чужой заказ, незачем), `INVALID_ARGUMENT` → `INVALID_ARGUMENT`, остальное (`UNAVAILABLE`, `Internal`) → `UNAVAILABLE` с логом причины. Пустой `restaurantId` отклоняется до вызова контекста.
5. **UI без optimistic update.** Состояние меняется только по ответу команды: при отказе экран не «откатывает» ложное состояние. Ответ сразу показывается из payload; urql document cache инвалидирует queries с `Restaurant` и перезапрашивает их. Повтор — только вручную: команды идемпотентны, автоматического retry нет (FR mutations `docs/ui.md`).
6. **Код ошибки в UI.** Cosmo Router кладёт gRPC-код плагина в `errors[].extensions.errors[].extensions.code` (`PermissionDenied`, `NotFound`, `Unauthenticated`), как проверяет `make kind-test` (7.6). Feature читает его в `bff-error.ts`; MSW-моки отвечают той же формой (`bffError`).
7. **Экран в customer UI.** FR-001 отделяет customer UI от будущего restaurant UI. Отдельного restaurant-приложения нет, а экран из одного действия не оправдывает новую boundary. Экран — изолированная feature под префиксом `/manage/`, без ссылок из customer-навигации; при выделении restaurant UI она переносится целиком. FR-001 изменён явно, чтобы исключение было зафиксировано.

## Risks / Trade-offs

- Браузер пока не получает access token (identity provider не выбран), поэтому с реальным BFF экран ответит `UNAUTHENTICATED` — как `placeOrder`. UI показывает «Войдите в аккаунт владельца».
- Нет query «мои рестораны»: владелец открывает экран по ссылке с id ресторана.
